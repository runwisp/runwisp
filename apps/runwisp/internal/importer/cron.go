// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"bufio"
	"io"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/cronspec"
	"github.com/runwisp/runwisp/internal/model"
)

// CronOptions tunes crontab parsing.
type CronOptions struct {
	// System indicates a system crontab (/etc/crontab, /etc/cron.d/*) whose
	// schedule is followed by a user column before the command. A per-user
	// `crontab -l` dump has no such column.
	System bool
	// Detect enables auto-detection of the system-crontab format when System is
	// false: the parser sniffs the classic `# … user command` header legend (and
	// switches to system parsing if found), and otherwise warns — via an inline
	// TODO banner — when a job line looks like it carries a user column. It is
	// left off when the operator forces the format with --system / --system=false.
	Detect bool
	// Existing carries the task/service names already defined in the live config's
	// OTHER files — the merged config minus the machine-owned staging file that a
	// re-import overwrites. It powers identity-aware dedup so re-importing after a
	// `promote` doesn't collide: a job whose derived name AND command match an
	// existing task is skipped (already owned by the root config); a name-only
	// clash is renamed to name-2 rather than duplicating. The value is the
	// existing task's run command, or "" for entries with no comparable command
	// (services, compose) which therefore always force a rename, never a skip.
	Existing map[string]string
}

// cronWrappers are leading command tokens that don't name the real program, so
// the name deriver skips past them. Flag tokens (starting with "-") and
// NAME=value assignments are skipped too. They double as part of the
// not-a-username denylist used when sniffing for a system-crontab user column.
var cronWrappers = map[string]bool{
	"sudo": true, "nice": true, "ionice": true, "flock": true,
	"env": true, "time": true, "exec": true, "nohup": true,
}

// cronInterpreters are common commands that can legitimately open a per-user job
// (`* * * * * python /app/x.py`) and so must never be mistaken for the user
// column of a system crontab. Combined with cronWrappers, this keeps the
// ambiguity warning from firing on ordinary per-user crontabs.
var cronInterpreters = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "fish": true, "dash": true,
	"python": true, "python2": true, "python3": true, "perl": true,
	"ruby": true, "node": true, "nodejs": true, "php": true, "deno": true,
	"curl": true, "wget": true, "make": true, "go": true, "java": true,
}

// ParseCrontab converts a crontab into a *Result. It never errors on bad
// content — malformed lines become Notes so the operator sees them rather than
// losing them. The io.Reader contract makes it trivial to feed `crontab -l`
// over a pipe.
func ParseCrontab(r io.Reader, opts CronOptions) (*Result, error) {
	cp := &crontabParser{
		res:      &Result{},
		dd:       newDeduper(),
		opts:     opts,
		existing: opts.Existing,
		// system can be flipped on mid-parse when Detect spots the header legend.
		system: opts.System,
		env:    map[string]string{},
	}
	// Reserve names the live config already owns so a name clash renames to
	// name-2 instead of emitting a duplicate that fails the merged load.
	for name := range opts.Existing {
		cp.dd.reserve(name)
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		cp.feedLine(sc.Text())
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	cp.warnIfAmbiguous()
	cp.assemble()
	return cp.res, nil
}

// crontabParser carries the running state of a single crontab parse so the
// per-line classification stays out of ParseCrontab's hot loop.
type crontabParser struct {
	res  *Result
	dd   *deduper
	opts CronOptions

	system    bool
	ambiguous bool

	existing       map[string]string // name → run of tasks already owned by other config files
	env            map[string]string
	shell          string
	timezone       string
	pendingComment string // a "# ..." line directly above a job
	jobs           []block
}

// feedLine classifies one raw crontab line and routes it to the right handler.
func (cp *crontabParser) feedLine(raw string) {
	line := strings.TrimSpace(raw)
	if line == "" {
		cp.pendingComment = ""
		return
	}
	if strings.HasPrefix(line, "#") {
		cp.handleComment(line)
		return
	}
	// Environment assignment: NAME = value, with an "=" before any
	// whitespace-separated command would appear. Cron treats these as
	// settings, not jobs.
	if name, value, ok := cronEnvLine(line); ok {
		cp.handleEnv(name, value)
		return
	}
	// In per-user mode, flag a line that looks like it smuggles a user column
	// rather than silently folding the username into the command.
	if cp.opts.Detect && !cp.system && looksLikeUserColumn(line) {
		cp.ambiguous = true
	}
	cp.handleJob(line)
}

func (cp *crontabParser) handleComment(line string) {
	comment := strings.TrimSpace(strings.TrimPrefix(line, "#"))
	// The classic `# m h dom mon dow user command` legend is a strong, safe
	// signal that this is a system crontab.
	if cp.opts.Detect && !cp.system && isCronHeaderLegend(comment) {
		cp.system = true
		cp.pendingComment = ""
		return
	}
	cp.pendingComment = comment
}

func (cp *crontabParser) handleEnv(name, value string) {
	switch strings.ToUpper(name) {
	case "SHELL":
		cp.shell = value
		if value != "" && !filepath.IsAbs(value) {
			cp.res.addNote(LevelAttention, "",
				"crontab SHELL="+value+" is not an absolute path; RunWisp needs an "+
					"absolute shell path. The imported tasks keep the default shell.")
		}
	case "MAILTO":
		cp.res.addNote(LevelAttention, "",
			"crontab sets MAILTO="+value+" — RunWisp doesn't email job output. "+
				"Wire a notifier instead (see notify_on_failure).")
	case "CRON_TZ", "TZ":
		cp.timezone = value
	default:
		cp.env[name] = value
	}
	cp.pendingComment = ""
}

func (cp *crontabParser) handleJob(line string) {
	blocks, ok := cp.buildJob(line)
	cp.pendingComment = ""
	if !ok {
		cp.res.addNote(LevelAttention, "",
			"couldn't parse crontab line: "+truncate(line, 60))
		return
	}
	cp.jobs = append(cp.jobs, blocks...)
}

func (cp *crontabParser) warnIfAmbiguous() {
	if !cp.ambiguous {
		return
	}
	cp.res.topComments = append(cp.res.topComments,
		"⚠ TODO: some lines may carry a user column (this looks like a system",
		"  crontab). If a `run = \"…\"` below begins with a username, re-run with",
		"  `runwisp import cron --system`.")
	cp.res.addNote(LevelAttention, "",
		"this looks like a system crontab (a user column between the schedule and "+
			"command). Re-run with --system to split out per-task users, or "+
			"--system=false to silence this if the commands really do start with that word.")
}

// assemble emits the job blocks in reading order. RunWisp has no daemon-wide
// crontab singletons: SHELL, CRON_TZ/TZ, and top-of-file env vars are folded
// onto the individual tasks in buildJob instead. That keeps every imported task
// self-contained — and safe to live in an included staging file, which the
// config loader forbids from setting the [defaults] / [scheduler] singletons.
func (cp *crontabParser) assemble() {
	cp.res.blocks = append(cp.res.blocks, cp.jobs...)
}

// resolveJobName picks the task name for a job, applying identity-aware dedup
// against the live config (populated on a two-tier re-import). It returns
// skip=true when the job is already owned by the root config (same derived name
// AND same command — typically a promoted job still in the crontab), and
// otherwise a unique name, renamed when a different command already claims the
// base. Both non-trivial outcomes leave an info note so the choice is never
// silent.
func (cp *crontabParser) resolveJobName(command string) (name string, skip bool) {
	base := deriveCronName(command)
	existingCmd, reserved := cp.existing[base]
	if reserved && existingCmd != "" && sameCommand(existingCmd, command) {
		cp.res.addNote(LevelInfo, base,
			"already defined in runwisp.toml with the same command — skipped re-importing it.")
		return "", true
	}
	name = cp.dd.unique(base)
	if reserved && name != base {
		cp.res.addNote(LevelInfo, name,
			"runwisp.toml already defines \""+base+"\" with a different command — imported this one as \""+name+"\".")
	}
	return name, false
}

// buildJob turns one schedule+command line into its [tasks.NAME] block plus,
// when the crontab set SHELL / CRON_TZ / env vars in effect above it, the
// matching per-task fields and a [tasks.NAME.env] child block. Cron applies
// those settings to every job that follows them in the file, so the state is
// snapshotted at the job's position rather than folded globally.
func (cp *crontabParser) buildJob(line string) ([]block, bool) {
	schedule, command, runOnStart, ok := splitCronScheduleCommand(line, cp.system)
	if !ok {
		return nil, false
	}

	name, skip := cp.resolveJobName(command)
	if skip {
		return nil, true
	}
	b := block{
		header: "tasks." + name,
		isItem: true,
		name:   name,
		kind:   "task",
	}
	if cp.pendingComment != "" {
		b.set("description", tomlString(cp.pendingComment))
	}

	cp.res.applyCronSchedule(&b, name, schedule, runOnStart)

	if cp.system {
		// The user column sat between the schedule and the command; re-split to
		// recover it cleanly.
		if user, ok := systemCronUser(line); ok && user != "" {
			b.set("user", tomlString(user))
		}
	}
	if cp.timezone != "" {
		b.set("timezone", tomlString(cp.timezone))
	}
	// A relative SHELL was already flagged in handleEnv; only an absolute path
	// becomes a per-task shell.
	if cp.shell != "" && filepath.IsAbs(cp.shell) {
		b.set("shell", tomlString(cp.shell))
	}

	b.set("run", tomlString(command))
	if command != "" && strings.Contains(command, "%") {
		cp.res.addNote(LevelInfo, name,
			"command contains '%' — in crontab that means a newline/stdin marker. "+
				"RunWisp passes the command to the shell verbatim; adjust if you relied on it.")
	}

	blocks := []block{b}
	if eb, ok := envBlock("tasks."+name+".env", cp.env); ok {
		blocks = append(blocks, eb)
	}
	return blocks, true
}

// splitCronScheduleCommand splits a crontab job line into its schedule and
// command. It handles both the `@keyword command` shorthand (where @reboot maps
// to run-on-start and @annually/@midnight normalize to their canonical names)
// and the classic five-field schedule, with an extra user column when system.
func splitCronScheduleCommand(line string, system bool) (schedule, command string, runOnStart, ok bool) {
	if strings.HasPrefix(line, "@") {
		tok, rest, ok := splitFields(line, 1)
		if !ok {
			return "", "", false, false
		}
		switch strings.ToLower(tok[0]) {
		case "@reboot":
			return "", rest, true, true
		case "@annually":
			return "@yearly", rest, false, true
		case "@midnight":
			return "@daily", rest, false, true
		default:
			return tok[0], rest, false, true
		}
	}

	nFields := 5
	if system {
		nFields = 6 // schedule (5) + user column
	}
	tok, rest, ok := splitFields(line, nFields)
	if !ok || rest == "" {
		return "", "", false, false
	}
	return strings.Join(tok[:5], " "), rest, false, true
}

// applyCronSchedule sets the schedule-related fields on b, validating a cron
// expression and flagging it for the operator when it doesn't parse.
func (r *Result) applyCronSchedule(b *block, name, schedule string, runOnStart bool) {
	if runOnStart {
		b.set("run_on_start", "true")
		b.schedule = "@reboot"
		b.lead = []string{"@reboot — runs once each time the daemon starts."}
		return
	}
	b.schedule = schedule
	if err := cronspec.Validate(schedule, ""); err != nil {
		b.setComment("cron", tomlString(schedule),
			"TODO: RunWisp couldn't parse this cron expression — fix it.")
		b.attention = true
		r.addNote(LevelAttention, name,
			"cron expression "+schedule+" didn't parse: "+err.Error())
		return
	}
	b.set("cron", tomlString(schedule))
}

// isCronHeaderLegend reports whether a comment is the column legend that
// system crontabs print above their jobs, e.g.
// `# m h dom mon dow user command`. Requiring the day-of-week/day-of-month
// field words alongside "user" and "command" keeps it from matching prose.
func isCronHeaderLegend(comment string) bool {
	words := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(comment)) {
		words[w] = true
	}
	if !words["user"] || !words["command"] {
		return false
	}
	return words["dom"] || words["dow"]
}

// looksLikeUserColumn reports whether a per-user-parsed line actually looks like
// a system crontab line (schedule + user + command). It is deliberately
// conservative: the first five fields must be a valid cron schedule and the
// sixth token must look like a username rather than a command, so an ordinary
// per-user job (`* * * * * python /app/x.py`) never trips it.
func looksLikeUserColumn(line string) bool {
	if strings.HasPrefix(line, "@") {
		return false
	}
	tok, rest, ok := splitFields(line, 6)
	if !ok || rest == "" {
		return false
	}
	if cronspec.Validate(strings.Join(tok[:5], " "), "") != nil {
		return false
	}
	return isLikelyUsername(tok[5])
}

// isLikelyUsername reports whether a token looks like a POSIX login name and is
// not a known interpreter or wrapper that could legitimately open a command.
func isLikelyUsername(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	if s[0] >= '0' && s[0] <= '9' {
		return false // leading digit → cron value, not a username
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return !cronWrappers[s] && !cronInterpreters[s]
}

// systemCronUser recovers the user column (6th field) of a system crontab line.
func systemCronUser(line string) (string, bool) {
	tok, _, ok := splitFields(line, 6)
	if !ok {
		return "", false
	}
	return tok[5], true
}

// cronEnvLine reports whether line is a cron environment assignment and, if so,
// returns its name and unquoted value. Cron assignments have the form
// `NAME = value` where NAME is a bare identifier and "=" precedes the value.
func cronEnvLine(line string) (name, value string, ok bool) {
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return "", "", false
	}
	name = strings.TrimSpace(line[:eq])
	if name == "" || strings.ContainsAny(name, " \t/") {
		return "", "", false
	}
	for _, rn := range name {
		if !(rn == '_' || (rn >= 'A' && rn <= 'Z') || (rn >= 'a' && rn <= 'z') || (rn >= '0' && rn <= '9')) {
			return "", "", false
		}
	}
	value = strings.TrimSpace(line[eq+1:])
	value = strings.Trim(value, `"'`)
	return name, value, true
}

// deriveCronName builds a readable task name from a command: it skips wrapper
// programs, flags, and env assignments, then uses the basename of the first
// real token (minus a script extension), sanitized to RunWisp's name rules.
func deriveCronName(command string) string {
	tokens := strings.Fields(command)
	base := firstPathLikeProgram(tokens)
	if base == "" {
		base = firstBareProgram(tokens)
	}
	if base == "" {
		base = "job"
	}
	if ext := filepath.Ext(base); ext != "" && len(ext) <= 5 {
		base = strings.TrimSuffix(base, ext)
	}
	base = model.SanitizeTaskName(base)
	base = strings.Trim(base, "_-")
	if base == "" {
		base = "job"
	}
	if len(base) > model.TaskNameMaxLength {
		base = base[:model.TaskNameMaxLength]
	}
	return base
}

// firstPathLikeProgram returns the basename of the first path-like token
// (contains "/") that names a real program rather than a wrapper — this skips
// past `nice -n 19 …` and lands on the script being run. Empty when none match.
func firstPathLikeProgram(tokens []string) string {
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") || !strings.Contains(t, "/") {
			continue
		}
		name := filepath.Base(t)
		if cronWrappers[name] {
			continue
		}
		return name
	}
	return ""
}

// firstBareProgram returns the basename of the first bare token that isn't a
// flag, assignment, wrapper, or a stray numeric flag value. Empty when none
// match.
func firstBareProgram(tokens []string) string {
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") || isEnvAssignment(t) || isAllDigits(t) {
			continue
		}
		name := filepath.Base(t)
		if cronWrappers[name] {
			continue
		}
		return name
	}
	return ""
}

// isAllDigits reports whether s is non-empty and entirely ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isEnvAssignment reports whether a token is a leading NAME=value pair (so the
// name deriver skips `FOO=bar mycmd`).
func isEnvAssignment(token string) bool {
	eq := strings.IndexByte(token, '=')
	if eq <= 0 {
		return false
	}
	slash := strings.IndexByte(token, '/')
	return slash < 0 || slash > eq
}

// splitFields returns the first n whitespace-separated tokens of s plus the
// remainder of the string after the nth token (leading space trimmed). ok is
// false when s has fewer than n tokens.
func splitFields(s string, n int) (tokens []string, rest string, ok bool) {
	tokens = make([]string, 0, n)
	i := 0
	for len(tokens) < n {
		// skip leading whitespace
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= len(s) {
			return tokens, "", false
		}
		start := i
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
		tokens = append(tokens, s[start:i])
	}
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return tokens, s[i:], true
}

// sameCommand reports whether an imported crontab command matches the run of a
// task the live config already owns. `promote` preserves the command verbatim,
// so trimmed equality reliably identifies "the same job" — the signal for
// skipping a re-import rather than renaming it.
func sameCommand(a, b string) bool {
	return strings.TrimSpace(a) == strings.TrimSpace(b)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
