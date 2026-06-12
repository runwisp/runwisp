// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	res := &Result{}
	dd := newDeduper()

	// system can be flipped on mid-parse when Detect spots the header legend.
	system := opts.System
	ambiguous := false

	env := map[string]string{}
	var shell, timezone string
	var pendingComment string // a "# ..." line directly above a job
	var jobs []block

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		if line == "" {
			pendingComment = ""
			continue
		}
		if strings.HasPrefix(line, "#") {
			comment := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			// The classic `# m h dom mon dow user command` legend is a strong,
			// safe signal that this is a system crontab.
			if opts.Detect && !system && isCronHeaderLegend(comment) {
				system = true
				pendingComment = ""
				continue
			}
			pendingComment = comment
			continue
		}

		// Environment assignment: NAME = value, with an "=" before any
		// whitespace-separated command would appear. Cron treats these as
		// settings, not jobs.
		if name, value, ok := cronEnvLine(line); ok {
			switch strings.ToUpper(name) {
			case "SHELL":
				shell = value
			case "MAILTO":
				res.addNote(LevelAttention, "",
					"crontab sets MAILTO="+value+" — RunWisp doesn't email job output. "+
						"Wire a notifier instead (see notify_on_failure).")
			case "CRON_TZ", "TZ":
				timezone = value
			default:
				env[name] = value
			}
			pendingComment = ""
			continue
		}

		// In per-user mode, flag a line that looks like it smuggles a user column
		// rather than silently folding the username into the command.
		if opts.Detect && !system && looksLikeUserColumn(line) {
			ambiguous = true
		}

		b, ok := res.parseCronJob(line, pendingComment, system, dd)
		pendingComment = ""
		if !ok {
			res.addNote(LevelAttention, "",
				"couldn't parse crontab line: "+truncate(line, 60))
			continue
		}
		jobs = append(jobs, b)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	if ambiguous {
		res.topComments = append(res.topComments,
			"⚠ TODO: some lines may carry a user column (this looks like a system",
			"  crontab). If a `run = \"…\"` below begins with a username, re-run with",
			"  `runwisp import cron --system`.")
		res.addNote(LevelAttention, "",
			"this looks like a system crontab (a user column between the schedule and "+
				"command). Re-run with --system to split out per-task users, or "+
				"--system=false to silence this if the commands really do start with that word.")
	}

	// Assemble blocks in reading order: scheduler tz, defaults, then jobs.
	if timezone != "" {
		tz := block{header: "scheduler"}
		tz.set("timezone", tomlString(timezone))
		tz.lead = []string{"crontab CRON_TZ/TZ became the daemon-wide scheduler timezone."}
		res.blocks = append(res.blocks, tz)
	}
	if shell != "" || len(env) > 0 {
		def := block{header: "defaults"}
		if shell != "" {
			if filepath.IsAbs(shell) {
				def.set("shell", tomlString(shell))
			} else {
				res.addNote(LevelAttention, "",
					"crontab SHELL="+shell+" is not an absolute path; RunWisp needs an "+
						"absolute shell path. Left out of [defaults].shell.")
			}
		}
		if len(def.fields) > 0 {
			res.blocks = append(res.blocks, def)
		}
		if eb, ok := envBlock("defaults.env", env); ok {
			eb.lead = []string{"Environment variables that sat at the top of the crontab."}
			res.blocks = append(res.blocks, eb)
		}
	}
	res.blocks = append(res.blocks, jobs...)
	return res, nil
}

// parseCronJob turns one schedule+command line into a [tasks.NAME] block.
func (r *Result) parseCronJob(line, comment string, system bool, dd *deduper) (block, bool) {
	var schedule, command string
	var runOnStart bool

	if strings.HasPrefix(line, "@") {
		tok, rest, ok := splitFields(line, 1)
		if !ok {
			return block{}, false
		}
		command = rest
		switch strings.ToLower(tok[0]) {
		case "@reboot":
			runOnStart = true
		case "@annually":
			schedule = "@yearly"
		case "@midnight":
			schedule = "@daily"
		default:
			schedule = tok[0]
		}
	} else {
		nFields := 5
		if system {
			nFields = 6 // schedule (5) + user column
		}
		tok, rest, ok := splitFields(line, nFields)
		if !ok || rest == "" {
			return block{}, false
		}
		schedule = strings.Join(tok[:5], " ")
		command = rest
	}

	name := dd.unique(deriveCronName(command))
	b := block{
		header: "tasks." + name,
		isItem: true,
		name:   name,
		kind:   "task",
	}
	if comment != "" {
		b.set("description", tomlString(comment))
	}

	switch {
	case runOnStart:
		b.set("run_on_start", "true")
		b.schedule = "@reboot"
		b.lead = []string{"@reboot — runs once each time the daemon starts."}
	default:
		b.schedule = schedule
		if err := cronspec.Validate(schedule, ""); err != nil {
			b.setComment("cron", tomlString(schedule),
				"TODO: RunWisp couldn't parse this cron expression — fix it.")
			b.attention = true
			r.addNote(LevelAttention, name,
				"cron expression "+schedule+" didn't parse: "+err.Error())
		} else {
			b.set("cron", tomlString(schedule))
		}
	}

	if system {
		// tok[5] held the user column; re-split to recover it cleanly.
		if user, ok := systemCronUser(line); ok && user != "" {
			b.set("user", tomlString(user))
		}
	}

	b.set("run", tomlString(command))
	if command != "" && strings.Contains(command, "%") {
		r.addNote(LevelInfo, name,
			"command contains '%' — in crontab that means a newline/stdin marker. "+
				"RunWisp passes the command to the shell verbatim; adjust if you relied on it.")
	}
	return b, true
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
	base := ""
	// First choice: the first path-like token (contains "/") that names a real
	// program rather than a wrapper — this skips past `nice -n 19 …` and lands
	// on the script being run.
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") || !strings.Contains(t, "/") {
			continue
		}
		name := filepath.Base(t)
		if cronWrappers[name] {
			continue
		}
		base = name
		break
	}
	// Fallback: first bare token that isn't a flag, assignment, wrapper, or a
	// stray numeric flag value.
	if base == "" {
		for _, t := range tokens {
			if strings.HasPrefix(t, "-") || isEnvAssignment(t) || isAllDigits(t) {
				continue
			}
			name := filepath.Base(t)
			if cronWrappers[name] {
				continue
			}
			base = name
			break
		}
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
