// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
)

// SystemdOptions tunes systemd unit parsing.
type SystemdOptions struct {
	// Existing carries the entries the live config already defines, so a re-import
	// skips a unit it already owns instead of colliding on the merged load.
	Existing Owned
	// UnitDirs are the directories systemd loads units from, highest priority
	// first. A unit's drop-ins (NAME.d/*.conf) are read from each of them, as
	// well as from the unit file's own directory: `systemctl edit` writes them
	// under /etc even for a unit a package ships in /usr/lib.
	UnitDirs []string
}

// ParseSystemdFiles converts one or more systemd `.service` and `.timer` unit
// files into a *Result. Each file is one unit, named after its filename
// (foo.service → foo), with its drop-ins applied. A timer and the service it
// starts become one scheduled task named after the timer; a service also passed
// as an argument is not imported a second time on its own.
func ParseSystemdFiles(paths []string, opts SystemdOptions) (*Result, error) {
	units := make([]systemdUnit, 0, len(paths))
	started := map[string]bool{} // absolute paths of services some timer starts
	for _, p := range paths {
		u, svcPath, err := readSystemdArg(p, opts.UnitDirs)
		if err != nil {
			return nil, err
		}
		if abs, err := filepath.Abs(svcPath); svcPath != "" && err == nil {
			started[abs] = true
		}
		units = append(units, u)
	}

	res := &Result{}
	names := newNamer(res, opts.Existing, "")
	for i, u := range units {
		if abs, err := filepath.Abs(paths[i]); err == nil && u.timer == nil && started[abs] {
			continue // imported as part of the timer that starts it
		}
		importSystemdUnit(names, u)
	}
	return res, nil
}

// readSystemdArg reads one unit argument. A timer comes back with the service it
// starts read in, and that service's path; a service comes back noting the
// timer beside it that starts it, if any.
func readSystemdArg(p string, dirs []string) (u systemdUnit, svcPath string, err error) {
	sections, dropIns, err := readSystemdUnit(p, dirs)
	if err != nil {
		return u, "", err
	}
	u = systemdUnit{sections: sections, dropIns: dropIns, name: systemdUnitName(p), source: filepath.Base(p)}
	timer := findSection(sections, "Timer")
	if timer == nil {
		if sibling := strings.TrimSuffix(p, ".service") + ".timer"; sibling != p {
			if _, err := os.Stat(sibling); err == nil {
				u.startedBy = filepath.Base(sibling)
			}
		}
		return u, "", nil
	}

	svcPath = systemdTimerService(p, timer)
	u.timer = sections
	var svcDropIns []string
	u.sections, svcDropIns, err = readSystemdUnit(svcPath, dirs)
	u.dropIns = append(u.dropIns, svcDropIns...)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		u.missingService = "the timer starts " + filepath.Base(svcPath) + ", but " + svcPath +
			" doesn't exist, so there is nothing to run. Add the run line by hand."
	case err != nil:
		return u, "", err
	case findSection(u.sections, "Service") == nil:
		u.missingService = "the timer starts " + svcPath + ", which has no [Service] section, so there is nothing to run."
	}
	return u, svcPath, nil
}

// readSystemdUnit reads a unit file with its drop-ins applied after it, the way
// systemd assembles it: every NAME.d/*.conf across dirs and the file's own
// directory, a file in an earlier dir masking one of the same name in a later
// one, applied in filename order. It also returns the drop-ins it read.
//
// ponytail: reads NAME.d/ only, not a template's foo@.service.d/ or the
// per-type service.d/ and timer.d/; `systemctl cat` piped in covers those.
func readSystemdUnit(path string, dirs []string) (sections []systemdSection, dropIns []string, err error) {
	sections, err = readSystemdFile(path)
	if err != nil {
		return nil, nil, err
	}
	if own := filepath.Dir(path); !slices.Contains(dirs, own) {
		dirs = append(slices.Clone(dirs), own)
	}
	byName := map[string]string{}
	for _, dir := range dirs {
		matches, _ := filepath.Glob(filepath.Join(dir, filepath.Base(path)+".d", "*.conf"))
		for _, m := range matches {
			if _, masked := byName[filepath.Base(m)]; !masked {
				byName[filepath.Base(m)] = m
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(byName)) {
		extra, err := readSystemdFile(byName[name])
		if err != nil {
			return nil, nil, err
		}
		sections = append(sections, extra...)
		dropIns = append(dropIns, byName[name])
	}
	return sections, dropIns, nil
}

// ParseSystemdReader converts a single unit read from r. A piped unit has no
// filename, so it can't be named after one — the row is imported under a
// placeholder name with a note telling the operator to set a real one.
func ParseSystemdReader(r io.Reader, opts SystemdOptions) (*Result, error) {
	sections, err := parseSystemdUnit(r)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	names := newNamer(res, opts.Existing, "")
	u := systemdUnit{sections: sections, source: "(stdin)"}
	if findSection(sections, "Timer") != nil {
		u.timer = sections
		u.missingService = "read from stdin, so there's no directory to find the service this timer starts in. " +
			"Add the run line by hand, or pass the timer as a file."
	}
	importSystemdUnit(names, u)
	return res, nil
}

func readSystemdFile(path string) ([]systemdSection, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseSystemdUnit(f)
}

// systemdTimerService is the path of the service a timer starts: its [Timer]
// Unit=, or else the service named after the timer, in the timer's directory.
func systemdTimerService(timerPath string, timer *systemdSection) string {
	unit := strings.TrimSpace(timer.last("Unit"))
	if unit == "" {
		unit = strings.TrimSuffix(filepath.Base(timerPath), ".timer") + ".service"
	}
	return filepath.Join(filepath.Dir(timerPath), unit)
}

// systemdUnitName turns a unit path into its base name: /etc/systemd/system/foo.service
// → "foo". A template unit (foo@.service) keeps the "@" so the mapper can flag it.
func systemdUnitName(path string) string {
	base := filepath.Base(path)
	for _, ext := range []string{".service", ".timer", ".target", ".socket"} {
		base = strings.TrimSuffix(base, ext)
	}
	return base
}

// --- unit file reader ---

type systemdKV struct{ key, value string }

// systemdSection is one [Section] and its directives in source order. Unlike the
// supervisord INI reader, duplicate keys are preserved: systemd accumulates
// repeated Environment=/ExecStart=/EnvironmentFile= lines, and collapsing them to
// the last value would silently drop the rest.
type systemdSection struct {
	name string
	kvs  []systemdKV
}

// all returns every value of key in order. An empty assignment resets the list,
// as systemd does for ExecStart=, Environment=, OnCalendar= and the rest of its
// list-valued directives: it is how a drop-in replaces rather than adds to one.
func (s *systemdSection) all(key string) []string {
	var out []string
	for _, kv := range s.kvs {
		if kv.key != key {
			continue
		}
		if kv.value == "" {
			out = nil
			continue
		}
		out = append(out, kv.value)
	}
	return out
}

func (s *systemdSection) last(key string) string {
	v := s.all(key)
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// parseSystemdUnit reads a systemd unit into sections. It handles full-line
// comments (# or ;), [Section] headers, Key=Value directives, and line
// continuation (a physical line ending in an unescaped backslash joins the next).
func parseSystemdUnit(r io.Reader) ([]systemdSection, error) {
	lines, err := foldSystemdLines(r)
	if err != nil {
		return nil, err
	}
	var sections []systemdSection
	var cur *systemdSection
	for _, logical := range lines {
		cur = classifySystemdLine(&sections, cur, logical)
	}
	return sections, nil
}

// foldSystemdLines returns the unit's logical lines: one physical line, or
// several joined across trailing backslashes. The first physical line keeps its
// Key= prefix; continuation lines are trimmed and joined with a single space,
// the way systemd folds an ExecStart split across lines back into one command.
func foldSystemdLines(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out []string
	var logical strings.Builder
	inCont := false
	for sc.Scan() {
		payload := strings.TrimRight(sc.Text(), " \t")
		// A comment line is never continued, even when it ends in a backslash,
		// and inside a continuation systemd skips it altogether.
		trimmed := strings.TrimSpace(payload)
		comment := strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";")
		if comment && inCont {
			continue
		}
		cont := !comment && strings.HasSuffix(payload, "\\")
		payload = strings.TrimRight(strings.TrimSuffix(payload, "\\"), " \t")
		if inCont {
			logical.WriteByte(' ')
			logical.WriteString(strings.TrimSpace(payload))
		} else {
			logical.WriteString(payload)
		}
		if cont {
			inCont = true
			continue
		}
		out = append(out, logical.String())
		logical.Reset()
		inCont = false
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if inCont {
		out = append(out, logical.String()) // trailing backslash at EOF
	}
	return out, nil
}

// classifySystemdLine folds one logical line into sections and returns the
// current section (unchanged for comments and directives, the new one for a
// header). A directive before any header, or a line with no '=', is ignored.
func classifySystemdLine(sections *[]systemdSection, cur *systemdSection, logical string) *systemdSection {
	trimmed := strings.TrimSpace(logical)
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
		return cur
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		*sections = append(*sections, systemdSection{name: strings.TrimSpace(trimmed[1 : len(trimmed)-1])})
		return &(*sections)[len(*sections)-1]
	}
	if cur == nil {
		return nil // directive before any section header
	}
	if key, value, ok := strings.Cut(trimmed, "="); ok && key != "" {
		cur.kvs = append(cur.kvs, systemdKV{
			key:   strings.TrimSpace(key),
			value: strings.TrimSpace(value),
		})
	}
	return cur
}

// --- mapper ---

// findSection returns every section with the given name merged into one, its
// directives in file order, or nil when there is none. systemd reads a repeated
// section (a drop-in's [Timer], or `systemctl cat` output) as more lines of the
// same one, so a later directive overrides or extends an earlier one rather
// than replacing the whole section.
func findSection(sections []systemdSection, name string) *systemdSection {
	var found *systemdSection
	for i := range sections {
		if sections[i].name != name {
			continue
		}
		if found == nil {
			found = &systemdSection{name: name}
		}
		found.kvs = append(found.kvs, sections[i].kvs...)
	}
	return found
}

// systemdSandboxKeys are the [Service] confinement directives RunWisp can't
// reproduce. Presence of any one means the imported service runs with more
// access than the unit granted it, so it's a blocking note rather than a silent
// drop.
var systemdSandboxKeys = map[string]bool{
	"ProtectSystem": true, "ProtectHome": true, "PrivateTmp": true,
	"PrivateDevices": true, "PrivateNetwork": true, "PrivateUsers": true,
	"NoNewPrivileges": true, "ProtectKernelTunables": true, "ProtectKernelModules": true,
	"ProtectControlGroups": true, "RestrictAddressFamilies": true, "RestrictNamespaces": true,
	"CapabilityBoundingSet": true, "AmbientCapabilities": true, "ReadOnlyPaths": true,
	"ReadWritePaths": true, "InaccessiblePaths": true, "DynamicUser": true,
	"SystemCallFilter": true, "MemoryDenyWriteExecute": true, "LockPersonality": true,
}

// systemdCosmeticKeys are [Service] directives RunWisp drops without a word:
// resource accounting, logging targets (captured automatically), and readiness
// timing that has no bearing on how the command runs.
var systemdCosmeticKeys = map[string]bool{
	"RemainAfterExit": true, "StandardInput": true, "SyslogIdentifier": true,
	"RestartSec": true, "StartLimitInterval": true, "StartLimitIntervalSec": true,
	"StartLimitBurst": true, "WatchdogSec": true, "OOMScoreAdjust": true,
	"LimitNOFILE": true, "LimitNPROC": true, "PIDFile": true, "GuessMainPID": true,
	"NotifyAccess": true,
}

// systemdUnit is one unit to import: a service, or a timer and the service it
// starts.
type systemdUnit struct {
	sections []systemdSection // the service unit; nil when a timer's service is missing
	timer    []systemdSection // the timer unit that starts the service, or nil
	// missingService is the note for a timer whose service has no [Service]
	// section to import, usually because the file isn't there.
	missingService string
	// dropIns are the drop-in files applied to the unit (and the timer's service).
	dropIns []string
	// startedBy is the sibling timer that starts a service imported on its own.
	startedBy    string
	name, source string
}

// importSystemdUnit maps one parsed unit onto a RunWisp task or service, opening
// a report row before anything about the mapping can go wrong.
func importSystemdUnit(names *namer, u systemdUnit) {
	svc := findSection(u.sections, "Service")
	noService := svc == nil
	if noService {
		svc = &systemdSection{} // still open a row so the empty unit is reported
	}

	kind := systemdKind(svc)
	if u.timer != nil {
		kind = model.KindTask // systemd ignores Restart= for a timer-run unit too
	}
	execStarts := svc.all("ExecStart")
	runLine, _ := systemdRunLine(execStarts)

	base := finalizeTaskName(u.name, "service")
	ref, name, skip := names.resolve(u.source, base, OwnedEntry{Kind: kind, Run: runLine, User: systemdUser(svc)}, 0)
	if skip {
		return
	}
	if u.name == "" {
		ref.note(NoteSystemdNoName,
			"read from stdin, so there's no filename to name this after — imported as \""+name+"\"; rename it before running.")
	}

	prefix, schedule := "services.", "service"
	if kind == model.KindTask {
		prefix, schedule = "tasks.", "@reboot"
	}
	b := block{header: prefix + name}

	if desc := systemdDescription(u.sections); desc != "" {
		b.set("description", tomlString(desc))
	} else if desc := systemdDescription(u.timer); desc != "" {
		b.set("description", tomlString(desc))
	}

	var run string
	if u.timer != nil && noService {
		b.setComment("run", tomlVerbatimString(""), "TODO: the timer's service wasn't found.")
		ref.note(NoteSystemdTimerNoService, u.missingService)
	} else {
		run = systemdApplyRun(&b, svc, ref, execStarts)
	}
	switch {
	case u.timer != nil:
		schedule = systemdApplyTimer(&b, findSection(u.timer, "Timer"), ref)
	case kind == model.KindTask:
		b.set("run_on_start", tomlString(string(model.RunOnStartBoot)))
		ref.note(NoteSystemdOneshot,
			"Type=oneshot → imported as a run-once-per-boot task (run_on_start = \"boot\").")
	}
	if u.startedBy != "" {
		runs := "as an always-on service"
		if kind == model.KindTask {
			runs = "once per boot"
		}
		ref.note(NoteSystemdStartedByTimer,
			u.startedBy+" starts this unit on a schedule, and imported on its own it runs "+runs+" instead. "+
				"Import the timer (`runwisp import systemd "+u.startedBy+"`) to keep the schedule.")
	}
	if len(u.dropIns) > 0 {
		ref.note(NoteSystemdDropIns, "applied the drop-ins "+strings.Join(u.dropIns, ", ")+".")
	}
	env := systemdApplyServiceKeys(&b, svc, ref, kind)
	systemdNoteUnitKeys(ref, findSection(u.sections, "Unit"))

	blocks := []block{b}
	if eb, ok := envBlock(prefix+name+".env", env); ok {
		blocks = append(blocks, eb)
	}
	ref.emit(name, kind, schedule, run, blocks...)
}

// systemdDescription is the unit's [Unit] Description=, or empty.
func systemdDescription(sections []systemdSection) string {
	if unit := findSection(sections, "Unit"); unit != nil {
		return strings.TrimSpace(unit.last("Description"))
	}
	return ""
}

// systemdKind decides whether a unit maps onto a RunWisp service or a run-once
// task. Type=oneshot is the run-once task; everything else is a long-running
// service. Restart is irrelevant to the choice: it maps onto the service's own
// restart key (see systemdApplyRestart).
func systemdKind(svc *systemdSection) model.TaskKind {
	if strings.EqualFold(svc.last("Type"), "oneshot") {
		return model.KindTask
	}
	return model.KindService
}

// systemdRunLine returns the run command a unit would import to (the first
// ExecStart, prefixes stripped), so identity dedup can compare it before the
// block is built. Pure — systemdApplyRun owns the notes.
func systemdRunLine(execStarts []string) (run string, stripped bool) {
	if len(execStarts) == 0 {
		return "", false
	}
	return stripExecPrefixes(execStarts[0])
}

// stripExecPrefixes removes systemd's ExecStart special prefixes (@, -, +, !,
// !!, :) and reports whether any were present. They change argv[0], privilege,
// or failure semantics RunWisp doesn't reproduce. The rest of the line is made
// safe for `sh -c`, see quoteExecForShell.
func stripExecPrefixes(cmd string) (string, bool) {
	trimmed := strings.TrimLeft(cmd, "@-+!:")
	trimmed = strings.TrimSpace(trimmed)
	return quoteExecForShell(trimmed), trimmed != strings.TrimSpace(cmd)
}

// quoteExecForShell rewrites an ExecStart line as an `sh -c` command that runs
// the same argv. It splits the line the way systemd does (quotes and C escapes
// anywhere in a word, a lone `;` chaining commands), resolves `%%` to `%`, and
// re-emits each argument single-quoted where the shell would otherwise act on
// it. systemd's own variable expansion is kept: `${VAR}` becomes `"${VAR}"`
// (one argument, wherever it sits in a word) and a whole-word `$VAR` stays bare
// (split on whitespace). A `$VAR` inside a word, which systemd leaves alone, and
// `$$` are literal. Other `%` specifiers are left in place for the operator.
func quoteExecForShell(cmd string) string {
	var out []string
	for _, w := range splitExecWords(cmd) {
		if w.sep {
			out = append(out, ";")
			continue
		}
		out = append(out, shellExecArg(strings.ReplaceAll(w.text, "%%", "%")))
	}
	return strings.Join(out, " ")
}

// execWord is one ExecStart word, unquoted and unescaped; sep marks a lone
// unquoted `;`, which systemd reads as a command separator.
type execWord struct {
	text string
	sep  bool
}

// splitExecWords splits an ExecStart line into words the way systemd does:
// quotes and C escapes may appear anywhere in a word.
func splitExecWords(cmd string) []execWord {
	var words []execWord
	for _, raw := range rawExecWords(cmd) {
		switch raw {
		case ";":
			words = append(words, execWord{sep: true})
		case `\;`: // systemd's escape for a literal lone semicolon
			words = append(words, execWord{text: ";"})
		default:
			words = append(words, execWord{text: unquoteExecWord(raw)})
		}
	}
	return words
}

// rawExecWords splits cmd on whitespace outside quotes, keeping each word as
// written.
func rawExecWords(cmd string) []string {
	var words []string
	start := -1 // byte offset where the current word began
	var quote byte
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if quote == 0 && (c == ' ' || c == '\t') {
			if start >= 0 {
				words = append(words, cmd[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
		switch {
		case c == '\\':
			i++ // an escaped quote or space doesn't end anything
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		}
	}
	if start >= 0 {
		words = append(words, cmd[start:])
	}
	return words
}

// unquoteExecWord drops the quotes from one raw word and decodes its escapes.
func unquoteExecWord(raw string) string {
	var w strings.Builder
	var quote byte
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case c == '\\' && i+1 < len(raw):
			i += unescapeExec(raw[i+1:], &w)
		default:
			w.WriteByte(c)
		}
	}
	return w.String()
}

// execEscapes are the single-character C escapes systemd decodes.
var execEscapes = map[byte]byte{'a': '\a', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v',
	'\\': '\\', '"': '"', '\'': '\'', 's': ' '}

// unescapeExec decodes the C escape at the start of s (just past the backslash)
// into w and returns how many bytes it consumed. An escape systemd doesn't know
// is kept with its backslash, as systemd does.
func unescapeExec(s string, w *strings.Builder) int {
	if r, ok := execEscapes[s[0]]; ok {
		w.WriteByte(r)
		return 1
	}
	if s[0] == 'x' && len(s) >= 3 {
		if v, err := strconv.ParseUint(s[1:3], 16, 8); err == nil {
			w.WriteByte(byte(v))
			return 3
		}
	}
	if s[0] >= '0' && s[0] <= '7' && len(s) >= 3 {
		if v, err := strconv.ParseUint(s[:3], 8, 8); err == nil {
			w.WriteByte(byte(v))
			return 3
		}
	}
	w.WriteByte('\\')
	w.WriteByte(s[0])
	return 1
}

// execVarName matches a whole-word `$VAR`, the only place systemd expands one;
// execBracedVar matches a `${VAR}` at the start of a string.
var (
	execVarName   = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)
	execBracedVar = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}`)
)

// shellExecArg renders one unquoted ExecStart argument for the shell.
func shellExecArg(arg string) string {
	if execVarName.MatchString(arg) {
		return arg
	}
	var out, lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out.WriteString(textutil.ShellWord(lit.String()))
			lit.Reset()
		}
	}
	for i := 0; i < len(arg); i++ {
		if arg[i] == '$' && i+1 < len(arg) {
			if arg[i+1] == '$' {
				lit.WriteByte('$')
				i++
				continue
			}
			if ref := execBracedVar.FindString(arg[i:]); ref != "" {
				flush()
				out.WriteString(`"` + ref + `"`)
				i += len(ref) - 1
				continue
			}
		}
		lit.WriteByte(arg[i])
	}
	flush()
	if out.Len() == 0 {
		return "''"
	}
	return out.String()
}

// systemdApplyRun sets the run line from ExecStart, flagging the cases RunWisp
// can't carry over: no command, more than one command, and unfilled template
// specifiers. Returns the command that will run.
func systemdApplyRun(b *block, svc *systemdSection, ref itemRef, execStarts []string) string {
	if len(execStarts) == 0 {
		b.setComment("run", tomlVerbatimString(""), "TODO: unit had no ExecStart.")
		ref.note(NoteSystemdNoExecStart, "the [Service] had no ExecStart, so there is nothing to run.")
		return ""
	}

	run, stripped := stripExecPrefixes(execStarts[0])
	if stripped {
		ref.note(NoteSystemdExecPrefix,
			"stripped a special ExecStart prefix (@, -, +, ! …) — it changed argv[0] or "+
				"privileges in a way RunWisp doesn't reproduce. Review the run line.")
	}
	if specs := systemdSpecifiers(execStarts[0]); len(specs) > 0 {
		ref.note(NoteSystemdTemplate,
			"ExecStart uses the systemd specifiers "+strings.Join(specs, ", ")+". systemd filled them in "+
				"before running the command; RunWisp runs them as written. Replace each with the value you meant.")
	}
	b.set("run", tomlVerbatimString(run))

	if rest := systemdOtherCommands(svc, execStarts); len(rest) > 0 {
		ref.note(NoteSystemdMultiExec,
			"the unit runs more than one command and RunWisp runs one, so only the first ExecStart was "+
				"imported. Fold these into the run line by hand: "+strings.Join(rest, "; ")+".")
	}
	return run
}

// systemdOtherCommands lists the unit's commands besides the first ExecStart,
// in the order systemd runs them, as "Key=command".
func systemdOtherCommands(svc *systemdSection, execStarts []string) []string {
	var rest []string
	add := func(key string, cmds []string) {
		for _, c := range cmds {
			rest = append(rest, key+"="+c)
		}
	}
	add("ExecStartPre", svc.all("ExecStartPre"))
	add("ExecStart", execStarts[1:])
	for _, key := range []string{"ExecStartPost", "ExecReload", "ExecStop", "ExecStopPost"} {
		add(key, svc.all(key))
	}
	return rest
}

// systemdSpecifierMeanings are what the common specifiers become, so the note
// can say what systemd actually ran.
var systemdSpecifierMeanings = map[byte]string{
	'T': "/tmp", 'V': "/var/tmp", 'n': "the full unit name", 'N': "the unit name",
	'p': "the unit name prefix", 'i': "the instance name", 'I': "the instance name",
	'h': "the user's home directory", 'u': "the user name", 'H': "the host name",
}

// systemdSpecifiers lists the %-specifiers in an ExecStart line once each, in
// order, with what each one means when that is known ("%T (/tmp)"). `%%` is a
// literal percent sign, not a specifier.
func systemdSpecifiers(cmd string) []string {
	var out []string
	seen := map[byte]bool{}
	for i := 0; i+1 < len(cmd); i++ {
		if cmd[i] != '%' {
			continue
		}
		c := cmd[i+1]
		i++
		if c == '%' || seen[c] || !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			continue
		}
		seen[c] = true
		spec := "%" + string(c)
		if meaning, ok := systemdSpecifierMeanings[c]; ok {
			spec += " (" + meaning + ")"
		}
		out = append(out, spec)
	}
	return out
}

// systemdApplyServiceKeys maps the [Service] directives RunWisp understands onto
// b and returns the parsed environment (nil when the unit set none). Anything it
// can't model becomes a note so nothing is silently dropped.
func systemdApplyServiceKeys(b *block, svc *systemdSection, ref itemRef, kind model.TaskKind) map[string]string {
	env := map[string]string{}
	var dropped []string
	var sawSandbox, sawSocket bool
	gracefulStop, runTimeout := "", ""

	for _, kv := range svc.kvs {
		switch kv.key {
		case "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "Type":
			// handled in systemdApplyRun / systemdKind
		case "WorkingDirectory":
			systemdApplyWorkingDir(b, ref, kv.value)
		case "User":
			// Group is folded in below, so skip a bare second write here.
		case "Group":
		case "Environment":
			parseSystemdEnvInto(env, kv.value)
		case "EnvironmentFile":
			// Emitted once, below, so multiple files can be reported together.
		case "KillSignal":
			systemdApplyKillSignal(b, ref, kv.key, kv.value)
		case "TimeoutStopSec", "TimeoutSec":
			// A later value overrides an earlier one, even one RunWisp can't read.
			gracefulStop = systemdStopTimeout(ref, kv.key, kv.value)
			if kv.key == "TimeoutSec" {
				runTimeout = kv.key + "=" + kv.value
			}
		case "TimeoutStartSec":
			runTimeout = kv.key + "=" + kv.value
		case "StandardOutput", "StandardError":
			systemdNoteOutputFile(ref, kv.key, kv.value)
		case "Restart":
			// Behavior note handled after the loop (needs the resolved kind).
		default:
			switch {
			case systemdSandboxKeys[kv.key]:
				sawSandbox = true
			case strings.HasPrefix(kv.key, "Listen") || kv.key == "Sockets":
				sawSocket = true
			case systemdCosmeticKeys[kv.key]:
				// dropped without a word
			default:
				dropped = append(dropped, kv.key)
			}
		}
	}

	if gracefulStop != "" {
		b.set("graceful_stop", tomlString(gracefulStop))
	}
	if runTimeout != "" && systemdKind(svc) == model.KindTask {
		systemdApplyRunTimeout(b, ref, runTimeout)
	}
	systemdApplyUser(b, svc)
	systemdApplyEnvFiles(b, svc, ref)
	systemdApplyType(svc, ref)
	systemdApplyRestart(b, svc, ref, kind)
	systemdNoteDropped(ref, dropped, sawSandbox, sawSocket)
	return env
}

func systemdApplyWorkingDir(b *block, ref itemRef, dir string) {
	b.set("working_dir", tomlString(dir))
	if !filepath.IsAbs(dir) {
		ref.note(NoteRelativeDirectory,
			"WorkingDirectory="+dir+" is relative; RunWisp resolves working_dir "+
				"against the runwisp.toml location.")
	}
}

// systemdNoteOutputFile notes a StandardOutput=/StandardError= that sent the
// output to a file: RunWisp captures it in the run log instead.
func systemdNoteOutputFile(ref itemRef, key, value string) {
	to, path, ok := strings.Cut(value, ":")
	if ok && (to == "file" || to == "append" || to == "truncate") {
		ref.note(NoteOutputRedirected,
			key+"="+value+": systemd wrote the output to "+path+"; RunWisp keeps it in the run's log instead.")
	}
}

// systemdStopTimeout reads a TimeoutStopSec/TimeoutSec value as graceful_stop,
// or "" (with a note) when there is nothing RunWisp can set.
func systemdStopTimeout(ref itemRef, key, value string) string {
	d, ok := systemdTimeSpan(value)
	switch {
	case ok && isZeroDuration(d):
		// systemd's 0 means "wait forever"; RunWisp's 0s kills at once, and
		// it has no wait-forever setting, so leave graceful_stop at its default.
		ref.note(NoteKeyUnreadable,
			key+"="+value+" means no timeout in systemd, but RunWisp has no such "+
				"setting (graceful_stop = \"0s\" would kill at once), so graceful_stop was left at its default.")
		return ""
	case ok:
		return d
	default:
		ref.note(NoteKeyUnreadable,
			key+"="+value+" isn't a duration RunWisp can read, so it was dropped.")
		return ""
	}
}

// systemdApplyRunTimeout maps a oneshot unit's TimeoutStartSec= (passed as
// "Key=value") onto timeout: for Type=oneshot the start timeout bounds the whole
// run. On any other type it only bounds startup, so the caller skips it. 0 and
// infinity mean no limit, which is RunWisp's default.
func systemdApplyRunTimeout(b *block, ref itemRef, setting string) {
	_, value, _ := strings.Cut(setting, "=")
	d, ok := systemdTimeSpan(value)
	switch {
	case ok && !isZeroDuration(d):
		b.set("timeout", tomlString(d))
	case !ok && !strings.EqualFold(strings.TrimSpace(value), "infinity"):
		ref.note(NoteKeyUnreadable, setting+" isn't a duration RunWisp can read, so it was dropped.")
	}
}

// systemdNoteUnitKeys notes the [Unit] directives that change what happens
// around a run: a unit started when this one finishes, and conditions that made
// systemd skip it. The rest of [Unit] (ordering, docs) has no bearing on the
// task.
func systemdNoteUnitKeys(ref itemRef, unit *systemdSection) {
	if unit == nil {
		return
	}
	var hooks, conditions []string
	for _, kv := range unit.kvs {
		switch {
		case kv.value == "":
		case kv.key == "OnFailure" || kv.key == "OnSuccess":
			hooks = append(hooks, kv.key+"="+kv.value)
		case strings.HasPrefix(kv.key, "Condition") || strings.HasPrefix(kv.key, "Assert"):
			conditions = append(conditions, kv.key+"="+kv.value)
		}
	}
	if len(hooks) > 0 {
		ref.note(NoteKeysUnsupported, strings.Join(hooks, ", ")+" started another unit after a run; RunWisp doesn't. "+
			"Use a notification route to hear about failures.")
	}
	if len(conditions) > 0 {
		ref.note(NoteKeysUnsupported, strings.Join(conditions, ", ")+": systemd skipped the run when this didn't hold, "+
			"RunWisp always runs it. Put the check in the run line if it matters.")
	}
}

// systemdNoteDropped emits the notes for directives that had no home in the
// RunWisp block: sandboxing, socket activation, and any leftover unknown keys.
func systemdNoteDropped(ref itemRef, dropped []string, sawSandbox, sawSocket bool) {
	if sawSandbox {
		ref.note(NoteSystemdSandbox,
			"the unit set sandboxing directives (ProtectSystem, PrivateTmp, …) — RunWisp "+
				"doesn't confine the process, so it runs with more access than the unit granted.")
	}
	if sawSocket {
		ref.note(NoteSystemdSocketActivation,
			"the unit looks socket-activated — RunWisp has no socket activation, so it won't "+
				"receive the listening socket systemd handed the service.")
	}
	if len(dropped) > 0 {
		ref.note(NoteKeysUnsupported,
			"these systemd directives have no RunWisp equivalent and were dropped: "+
				strings.Join(dropped, ", ")+".")
	}
}

// systemdApplyKillSignal maps KillSignal= to stop_signal, noting anything
// outside RunWisp's allowlist instead of writing an invalid value.
func systemdApplyKillSignal(b *block, ref itemRef, key, value string) {
	canonical, ok := model.NormalizeSignalName(value)
	if !ok {
		ref.note(NoteKeyUnreadable, key+"="+value+" isn't a signal RunWisp can read, so it was dropped.")
		return
	}
	b.set("stop_signal", tomlString(canonical))
}

func systemdApplyUser(b *block, svc *systemdSection) {
	if user := systemdUser(svc); user != "" {
		b.set("user", tomlString(user))
	}
}

// systemdUser is the unit's User[:Group], or empty when it sets no User.
func systemdUser(svc *systemdSection) string {
	user := strings.TrimSpace(svc.last("User"))
	if user == "" {
		return ""
	}
	if group := strings.TrimSpace(svc.last("Group")); group != "" {
		user += ":" + group
	}
	return user
}

func systemdApplyEnvFiles(b *block, svc *systemdSection, ref itemRef) {
	var usable []string
	for _, raw := range svc.all("EnvironmentFile") {
		// A leading "-" marks the file optional in systemd. RunWisp's env_file must
		// exist, so an optional file is only imported when it's there to be read.
		path, optional := strings.CutPrefix(strings.TrimSpace(raw), "-")
		path = strings.TrimSpace(path)
		if _, err := os.Stat(path); optional && err != nil {
			ref.note(NoteSystemdEnvFileMissing,
				"EnvironmentFile=-"+path+" doesn't exist, and RunWisp's env_file must, so it "+
					"was left out. Add env_file once the file exists.")
			continue
		}
		usable = append(usable, path)
	}
	if len(usable) == 0 {
		return
	}
	b.set("env_file", tomlString(usable[0]))
	if len(usable) > 1 {
		ref.note(NoteSystemdEnvFileMulti,
			"the unit set multiple EnvironmentFile= lines — RunWisp takes one env_file, so "+
				"only the first was imported. Merge the rest by hand.")
	}
}

func systemdApplyType(svc *systemdSection, ref itemRef) {
	typ := strings.ToLower(strings.TrimSpace(svc.last("Type")))
	if typ == "notify" || typ == "notify-reload" || typ == "forking" || typ == "dbus" {
		ref.note(NoteSystemdType,
			"Type="+typ+" relies on a readiness protocol or a forked main PID RunWisp "+
				"doesn't track — it supervises the process it starts and treats it as up "+
				"immediately. Confirm that's acceptable.")
	}
}

// systemdRestartPolicy maps Restart= onto a RunWisp restart value. exact is
// false for the values RunWisp has no equal for (on-success, on-abnormal,
// on-abort, on-watchdog), which land on the nearest policy with a note. None of
// them restarts after a failure, so neither does the mapped policy.
func systemdRestartPolicy(restart string) (policy model.RestartPolicy, exact bool) {
	switch restart {
	case "always":
		return model.RestartAlways, true
	case "", "no":
		return model.RestartNever, true
	case "on-failure":
		return model.RestartOnFailure, true
	case "on-success":
		return model.RestartNever, false
	default: // on-abnormal, on-abort, on-watchdog: restart after a bad ending only
		return model.RestartOnFailure, false
	}
}

// systemdApplyRestart maps Restart= for a service. "always" is RunWisp's
// default, so it emits no key.
func systemdApplyRestart(b *block, svc *systemdSection, ref itemRef, kind model.TaskKind) {
	if kind != model.KindService {
		return
	}
	restart := strings.ToLower(strings.TrimSpace(svc.last("Restart")))
	policy, exact := systemdRestartPolicy(restart)
	if policy != model.RestartAlways {
		b.set("restart", tomlString(string(policy)))
	}
	if !exact {
		ref.note(NoteSystemdRestartBehavior,
			"Restart="+restart+" has no exact RunWisp equivalent; imported as restart = \""+
				string(policy)+"\". Check that it restarts when you expect.")
	}
}

// systemdTimeUnits are the units systemd accepts in a time span, in seconds.
// Case matters: "m" is a minute, "M" a month.
var systemdTimeUnits = map[string]float64{
	"": 1, "s": 1, "sec": 1, "second": 1, "seconds": 1,
	"us": 1e-6, "µs": 1e-6, "usec": 1e-6, "ms": 1e-3, "msec": 1e-3,
	"m": 60, "min": 60, "minute": 60, "minutes": 60,
	"h": 3600, "hr": 3600, "hour": 3600, "hours": 3600,
	"d": 86400, "day": 86400, "days": 86400,
	"w": 604800, "week": 604800, "weeks": 604800,
	"M": 2629800, "month": 2629800, "months": 2629800,
	"y": 31557600, "year": 31557600, "years": 31557600,
}

// systemdTimeSpan turns a systemd time span ("90", "5min", "1d", "1h 30min",
// "1.5h") into a RunWisp duration ("1m30s", "5m", "24h", "1h30m", "1h30m"). It
// returns false for "infinity", an empty value, and anything systemd wouldn't
// read either.
func systemdTimeSpan(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", false
	}
	isNum := func(r rune) bool { return r >= '0' && r <= '9' || r == '.' }
	var total float64
	for v != "" {
		i := strings.IndexFunc(v, func(r rune) bool { return !isNum(r) })
		if i < 0 {
			i = len(v)
		}
		n, err := strconv.ParseFloat(v[:i], 64)
		if err != nil {
			return "", false
		}
		v = strings.TrimLeft(v[i:], " \t")
		j := strings.IndexFunc(v, func(r rune) bool { return isNum(r) || r == ' ' || r == '\t' })
		if j < 0 {
			j = len(v)
		}
		unit, ok := systemdTimeUnits[v[:j]]
		if !ok {
			return "", false
		}
		total += n * unit
		v = strings.TrimLeft(v[j:], " \t")
	}
	d := time.Duration(total * float64(time.Second)).Round(time.Microsecond)
	// time.Duration's own form, minus the zero units it always spells out:
	// 24h0m0s → 24h, 1h30m0s → 1h30m.
	str := d.String()
	if strings.HasSuffix(str, "m0s") {
		str = strings.TrimSuffix(str, "0s")
	}
	if strings.HasSuffix(str, "h0m") {
		str = strings.TrimSuffix(str, "0m")
	}
	return str, true
}

func isZeroDuration(d string) bool {
	dur, err := time.ParseDuration(d)
	return err == nil && dur == 0
}

// parseSystemdEnvInto parses one Environment= value — space-separated KEY=VALUE
// assignments, values optionally quoted — into env. Multiple Environment= lines
// accumulate because parseSystemdUnit preserves them and this merges each in;
// an empty one resets the list, as in systemd.
//
// ponytail: handles the common quoting (a whole assignment or its value wrapped
// in " or '); systemd's full C-escape grammar (\n, \x41, nested quotes) isn't
// reproduced — a value that relies on it lands verbatim for the operator to fix.
func parseSystemdEnvInto(env map[string]string, value string) {
	if value == "" {
		clear(env)
		return
	}
	for _, tok := range splitSystemdEnv(value) {
		tok = textutil.TrimMatchedQuotes(tok) // handles Environment="VAR=value with spaces"
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(tok[:eq])
		val := strings.TrimSpace(tok[eq+1:])
		val = textutil.TrimMatchedQuotes(val)
		if key != "" {
			env[key] = val
		}
	}
}

// splitSystemdEnv splits an Environment= line into assignments on whitespace
// that falls outside quotes.
func splitSystemdEnv(s string) []string {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
			cur.WriteByte(c)
		case c == ' ' || c == '\t':
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}
