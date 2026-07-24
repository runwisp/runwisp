// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// SupervisordOptions tunes supervisord parsing.
type SupervisordOptions struct {
	// Existing carries the entries the live config already defines outside the
	// machine-owned staging file that a re-import overwrites, so importing the
	// same supervisord config twice after a `promote` skips the program it
	// already owns instead of colliding on the merged load. See Owned.
	Existing Owned
}

// ParseSupervisordFiles converts one or more supervisord config files into a
// *Result, following [include] sections relative to each file's directory.
func ParseSupervisordFiles(paths []string, opts SupervisordOptions) (*Result, error) {
	sd := newSupervisordState(opts)
	for _, p := range paths {
		if err := sd.loadFile(p); err != nil {
			return nil, err
		}
	}
	return sd.finish(), nil
}

// ParseSupervisordReader converts a single supervisord config read from r.
// [include] directives can't be resolved without a base directory, so they
// surface as a Note rather than being followed.
func ParseSupervisordReader(r io.Reader, opts SupervisordOptions) (*Result, error) {
	sections, err := parseINI(r)
	if err != nil {
		return nil, err
	}
	sd := newSupervisordState(opts)
	sd.collect(sections, "")
	return sd.finish(), nil
}

type supervisordState struct {
	res      *Result
	names    *namer
	sections []iniSection
	visited  map[string]bool
}

func newSupervisordState(opts SupervisordOptions) *supervisordState {
	res := &Result{}
	return &supervisordState{
		res:     res,
		names:   newNamer(res, opts.Existing),
		visited: map[string]bool{},
	}
}

func (sd *supervisordState) loadFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if sd.visited[abs] {
		return nil // include cycle / duplicate
	}
	sd.visited[abs] = true

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sections, err := parseINI(f)
	if err != nil {
		return err
	}
	sd.collect(sections, filepath.Dir(abs))
	return nil
}

// collect appends sections in reading order, expanding [include] inline so the
// final section list mirrors what supervisord would assemble.
func (sd *supervisordState) collect(sections []iniSection, baseDir string) {
	for i := range sections {
		s := sections[i]
		if s.name == "include" {
			sd.expandInclude(&s, baseDir)
			continue
		}
		sd.sections = append(sd.sections, s)
	}
}

func (sd *supervisordState) expandInclude(s *iniSection, baseDir string) {
	files, ok := s.get("files")
	if !ok {
		return
	}
	if baseDir == "" {
		sd.res.addNote(LevelAttention, "",
			"[include] files="+files+" — can't resolve includes when reading from stdin. "+
				"Run `runwisp import supervisord <file>` against the config file instead.")
		return
	}
	for _, pattern := range strings.Fields(files) {
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) == 0 {
			sd.res.addNote(LevelAttention, "",
				"[include] pattern "+pattern+" matched no files.")
			continue
		}
		for _, m := range matches {
			if err := sd.loadFile(m); err != nil {
				sd.res.addNote(LevelAttention, "",
					"couldn't read included file "+m+": "+err.Error())
			}
		}
	}
}

// finish processes the collected sections into blocks: programs become
// services, groups annotate their members, and daemon-level sections are
// skipped with a note.
func (sd *supervisordState) finish() *Result {
	groupOf := sd.buildGroupMap()
	for i := range sd.sections {
		s := &sd.sections[i]
		kind, name := splitSectionName(s.name)
		switch kind {
		case "program":
			sd.processProgram(name, s, groupOf[name])
		case "group":
			sd.res.addNote(LevelInfo, name,
				"[group:"+name+"] — RunWisp has no program groups; its members were "+
					"imported individually and tagged with the group name.")
		case "eventlistener", "fcgi-program":
			sd.res.addNote(LevelAttention, name,
				"["+s.name+"] isn't supported — RunWisp has no event listeners or "+
					"FastCGI process manager. Skipped.")
		case "supervisord", "supervisorctl", "unix_http_server", "inet_http_server", "rpcinterface":
			sd.res.addNote(LevelInfo, "",
				"["+s.name+"] is supervisord daemon config with no RunWisp equivalent. "+
					"Skipped — RunWisp's daemon is configured in [daemon]/[server].")
		default:
			sd.res.addNote(LevelInfo, s.name,
				"["+s.name+"] wasn't recognized and was skipped.")
		}
	}
	return sd.res
}

// buildGroupMap maps a program name to its group name (last group wins, as in
// supervisord) by scanning [group:*] programs= lists.
func (sd *supervisordState) buildGroupMap() map[string]string {
	groupOf := map[string]string{}
	for i := range sd.sections {
		s := &sd.sections[i]
		kind, name := splitSectionName(s.name)
		if kind != "group" {
			continue
		}
		programs, ok := s.get("programs")
		if !ok {
			continue
		}
		for _, p := range strings.Split(programs, ",") {
			if p = strings.TrimSpace(p); p != "" {
				groupOf[p] = name
			}
		}
	}
	return groupOf
}

func (sd *supervisordState) processProgram(rawName string, s *iniSection, group string) {
	taskKind := programKind(s)
	name, skip := sd.names.resolve(sanitizeProgramName(rawName), taskKind, programCommand(s, rawName))
	if skip {
		return
	}
	isService := taskKind.IsService()
	sd.noteKindChoice(s, name)

	prefix, kind, schedule := "services.", "service", "service"
	if !isService {
		prefix, kind, schedule = "tasks.", "task", "@reboot"
	}
	b := block{header: prefix + name, isItem: true, name: name, kind: kind, schedule: schedule}

	if group != "" {
		b.set("group", tomlString(group))
	}
	sd.applyCommand(&b, s, name, rawName)
	if !isService {
		sd.applyRunOnce(&b, s, name)
	}
	env := sd.applyProgramKeys(&b, s, name, isService)

	sd.res.blocks = append(sd.res.blocks, b)
	if eb, ok := envBlock(prefix+name+".env", env); ok {
		sd.res.blocks = append(sd.res.blocks, eb)
	}
}

// programKind decides whether a [program] maps onto a RunWisp service.
// supervisord programs are long-running and restart by default, which maps onto
// a RunWisp service (services are always-on and always restart). The one
// exception is autorestart=false: that program runs once and is left alone,
// which is a run-once task, not a service. supervisord's own default is
// "unexpected", so an omitted autorestart still means service.
//
// Pure, so the kind is available for identity dedup before any note is emitted —
// noteKindChoice explains the non-obvious case once the final name is known.
func programKind(s *iniSection) model.TaskKind {
	v, ok := s.get("autorestart")
	if !ok {
		return model.KindService
	}
	if strings.EqualFold(strings.TrimSpace(v), "false") {
		return model.KindTask
	}
	return model.KindService
}

// noteKindChoice explains an autorestart value whose mapping isn't obvious.
func (sd *supervisordState) noteKindChoice(s *iniSection, name string) {
	v, ok := s.get("autorestart")
	if !ok || !strings.EqualFold(strings.TrimSpace(v), "unexpected") {
		return
	}
	sd.res.addNote(LevelInfo, name,
		"autorestart=unexpected → imported as an always-on service. RunWisp "+
			"services restart on any exit, not only unexpected ones; set "+
			"exit_codes if some non-zero codes should count as success.")
}

// programCommand returns the run line a program would import to, so identity
// dedup can compare it before the block is built. Pure — applyCommand owns
// emitting the notes and setting the field.
func programCommand(s *iniSection, rawName string) string {
	command, ok := s.get("command")
	if !ok || strings.TrimSpace(command) == "" {
		return ""
	}
	expanded, _ := expandSupervisordTokens(command, rawName)
	return expanded
}

// applyCommand sets the run line from the program's command=, expanding the
// %(program_name)s token and flagging unresolved supervisord expansions.
func (sd *supervisordState) applyCommand(b *block, s *iniSection, name, rawName string) {
	command, hasCmd := s.get("command")
	if !hasCmd || strings.TrimSpace(command) == "" {
		b.setComment("run", tomlString(""), "TODO: original [program] had no command.")
		b.attention = true
		sd.res.addNote(LevelAttention, name, "program has no command= line.")
		return
	}
	expanded, unresolved := expandSupervisordTokens(command, rawName)
	if unresolved {
		sd.res.addNote(LevelAttention, name,
			"command uses supervisord %(...)s expansions RunWisp doesn't fill in "+
				"(e.g. %(ENV_x)s, %(process_num)s). Review the run line.")
		b.attention = true
	}
	b.set("run", tomlString(expanded))
}

// applyRunOnce configures a run-once task: fire at boot (honoring autostart) and
// never restart.
func (sd *supervisordState) applyRunOnce(b *block, s *iniSection, name string) {
	runOnStart := true
	if v, ok := s.get("autostart"); ok {
		if parsed, valid := parseBool(v); valid {
			runOnStart = parsed
		}
	}
	if runOnStart {
		b.set("run_on_start", "true")
	}
	b.set("restart", tomlString(string(model.RestartNever)))
	sd.res.addNote(LevelInfo, name,
		"autorestart=false → imported as a run-once task (run_on_start, "+
			"restart=never), since RunWisp services always restart.")
}

// applyProgramKeys iterates the program's keys in source order, mapping the ones
// RunWisp understands onto b, and returns the parsed environment (nil when the
// program has no environment= line). Keys that only make sense for an always-on
// service are dropped with a note when the program landed as a task.
func (sd *supervisordState) applyProgramKeys(b *block, s *iniSection, name string, isService bool) map[string]string {
	var env map[string]string
	loggedLogNote := false
	for _, key := range s.keys {
		value, _ := s.get(key)
		switch key {
		case "command", "programs", "autorestart":
			// handled above / not applicable
		case "autostart":
			sd.applyAutostart(b, value, isService)
		case "directory":
			sd.applyDirectory(b, value, name)
		case "user":
			b.set("user", tomlString(value))
		case "umask":
			b.set("umask", tomlString(value))
		case "stopsignal":
			b.set("stop_signal", tomlString(normalizeSignal(value)))
		case "stopwaitsecs":
			if d, ok := secondsValue(value); ok {
				b.set("graceful_stop", tomlString(d))
			}
		case "exitcodes":
			if codes, ok := parseExitCodes(value); ok {
				b.set("exit_codes", tomlIntArray(codes))
			}
		case "environment":
			env = parseSupervisordEnv(value)
		case "priority", "startsecs", "startretries", "numprocs":
			sd.applyServiceKey(b, key, value, name, isService)
		case "stdout_logfile", "stderr_logfile", "redirect_stderr", "stdout_logfile_maxbytes", "stderr_logfile_maxbytes":
			// Captured automatically — note once, on the first log key seen.
			if !loggedLogNote {
				sd.res.addNote(LevelInfo, name,
					"log file settings were dropped — RunWisp captures stdout and stderr "+
						"per run automatically (see log_max_size / keep_runs to tune).")
				loggedLogNote = true
			}
		case "stopasgroup", "killasgroup":
			sd.res.addNote(LevelInfo, name,
				key+"="+value+" has no direct RunWisp equivalent; RunWisp signals the "+
					"process and its children on stop.")
		default:
			// Quietly ignore the long tail of supervisord knobs.
		}
	}
	return env
}

func (sd *supervisordState) applyAutostart(b *block, value string, isService bool) {
	if !isService {
		return // task case folded into run_on_start above
	}
	if v, ok := parseBool(value); ok {
		b.set("autostart", strconv.FormatBool(v))
	}
}

func (sd *supervisordState) applyDirectory(b *block, value, name string) {
	b.set("working_dir", tomlString(value))
	if !filepath.IsAbs(value) {
		sd.res.addNote(LevelInfo, name,
			"directory="+value+" is relative; RunWisp resolves working_dir "+
				"against the runwisp.toml location.")
	}
}

// applyServiceKey maps the supervisord keys that only make sense for an
// always-on service. When the program imported as a run-once task the key is
// dropped (with a note) by serviceOnly.
func (sd *supervisordState) applyServiceKey(b *block, key, value, name string, isService bool) {
	if !sd.serviceOnly(key, name, isService) {
		return
	}
	switch key {
	case "priority":
		if n, err := strconv.Atoi(value); err == nil {
			b.set("priority", strconv.Itoa(n))
		}
	case "startsecs":
		if d, ok := secondsValue(value); ok {
			b.set("healthy_after", tomlString(d))
		}
	case "startretries":
		if n, err := strconv.Atoi(value); err == nil {
			b.set("start_retries", strconv.Itoa(n))
		}
	case "numprocs":
		sd.applyNumprocs(b, value, name)
	}
}

func (sd *supervisordState) applyNumprocs(b *block, value, name string) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return
	}
	b.set("instances", strconv.Itoa(n))
	if n > 1 {
		sd.res.addNote(LevelInfo, name,
			"numprocs="+value+" became instances. Each instance gets a distinct "+
				"RUNWISP_INSTANCE_INDEX (0-based) in place of %(process_num)s.")
	}
}

// serviceOnly reports whether a service-only supervisord key applies. When the
// program was imported as a run-once task it returns false and leaves a note so
// the dropped setting isn't silently lost.
func (sd *supervisordState) serviceOnly(key, name string, isService bool) bool {
	if isService {
		return true
	}
	sd.res.addNote(LevelInfo, name,
		key+" was dropped — it only applies to always-on services, and this "+
			"program imported as a run-once task (autorestart=false).")
	return false
}

// splitSectionName splits "program:web" into ("program", "web"). A bare
// section name like "supervisord" returns ("supervisord", "").
func splitSectionName(s string) (kind, name string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], strings.TrimSpace(s[i+1:])
	}
	return s, ""
}

func sanitizeProgramName(name string) string {
	n := model.SanitizeTaskName(strings.TrimSpace(name))
	n = strings.Trim(n, "_-")
	if n == "" {
		n = "program"
	}
	if len(n) > model.TaskNameMaxLength {
		n = n[:model.TaskNameMaxLength]
	}
	return n
}

func parseBool(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true, true
	case "false", "0", "no", "off":
		return false, true
	}
	return false, false
}

// secondsValue turns a supervisord seconds count into a RunWisp duration like
// "10s". Returns false when the value isn't an integer.
func secondsValue(value string) (string, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return "", false
	}
	return strconv.Itoa(n) + "s", true
}

// normalizeSignal turns supervisord's "TERM" into RunWisp's "SIGTERM" form.
func normalizeSignal(value string) string {
	v := strings.ToUpper(strings.TrimSpace(value))
	if v == "" {
		return v
	}
	if !strings.HasPrefix(v, "SIG") {
		v = "SIG" + v
	}
	return v
}

func parseExitCodes(value string) ([]int, bool) {
	parts := strings.Split(value, ",")
	codes := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, false
		}
		codes = append(codes, n)
	}
	if len(codes) == 0 {
		return nil, false
	}
	return codes, true
}

// expandSupervisordTokens resolves the %(program_name)s expansion and reports
// whether any expansions RunWisp can't fill (%(ENV_x)s, %(process_num)s, …)
// remain in the string.
func expandSupervisordTokens(s, programName string) (string, bool) {
	s = strings.ReplaceAll(s, "%(program_name)s", programName)
	unresolved := strings.Contains(s, "%(")
	return s, unresolved
}

// parseSupervisordEnv parses a supervisord `environment=` value of the form
// KEY="value",KEY2=value into a map, honoring quotes around values that may
// contain commas.
func parseSupervisordEnv(value string) map[string]string {
	env := map[string]string{}
	var key strings.Builder
	var val strings.Builder
	inKey := true
	inQuote := byte(0)

	flush := func() {
		k := strings.TrimSpace(key.String())
		if k != "" {
			env[k] = strings.TrimSpace(val.String())
		}
		key.Reset()
		val.Reset()
		inKey = true
	}

	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case inQuote != 0:
			if c == inQuote {
				inQuote = 0
			} else {
				val.WriteByte(c)
			}
		case (c == '"' || c == '\'') && !inKey:
			inQuote = c
		case c == '=' && inKey:
			inKey = false
		case c == ',':
			flush()
		case inKey:
			key.WriteByte(c)
		default:
			val.WriteByte(c)
		}
	}
	flush()
	return env
}
