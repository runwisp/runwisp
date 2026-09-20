// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// SystemdOptions tunes systemd unit parsing.
type SystemdOptions struct {
	// Existing carries the entries the live config already defines, so a re-import
	// skips a unit it already owns instead of colliding on the merged load.
	Existing Owned
}

// ParseSystemdFiles converts one or more systemd `.service` unit files into a
// *Result. Each file is exactly one unit; its RunWisp name comes from the
// filename (foo.service → foo).
func ParseSystemdFiles(paths []string, opts SystemdOptions) (*Result, error) {
	res := &Result{}
	names := newNamer(res, opts.Existing, "")
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		sections, err := parseSystemdUnit(f)
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		importSystemdUnit(names, sections, systemdUnitName(p), filepath.Base(p))
	}
	return res, nil
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
	importSystemdUnit(names, sections, "", "(stdin)")
	return res, nil
}

// systemdUnitName turns a unit path into its base name: /etc/systemd/system/foo.service
// → "foo". A template unit (foo@.service) keeps the "@" so the mapper can flag it.
func systemdUnitName(path string) string {
	base := filepath.Base(path)
	for _, ext := range []string{".service", ".target", ".socket"} {
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

func (s *systemdSection) all(key string) []string {
	var out []string
	for _, kv := range s.kvs {
		if kv.key == key {
			out = append(out, kv.value)
		}
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
		cont := strings.HasSuffix(payload, "\\")
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
	if eq := strings.IndexByte(trimmed, '='); eq > 0 {
		cur.kvs = append(cur.kvs, systemdKV{
			key:   strings.TrimSpace(trimmed[:eq]),
			value: strings.TrimSpace(trimmed[eq+1:]),
		})
	}
	return cur
}

// --- mapper ---

// findSection returns the last section with the given name, or nil. systemd
// treats a directive from a later duplicate section as an override, so "last
// wins" matches the reader that assembled the unit.
func findSection(sections []systemdSection, name string) *systemdSection {
	var found *systemdSection
	for i := range sections {
		if sections[i].name == name {
			found = &sections[i]
		}
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
	"RemainAfterExit": true, "StandardOutput": true, "StandardError": true,
	"StandardInput": true, "SyslogIdentifier": true, "TimeoutStartSec": true,
	"RestartSec": true, "StartLimitInterval": true, "StartLimitIntervalSec": true,
	"StartLimitBurst": true, "WatchdogSec": true, "SuccessExitStatus": true,
	"Nice": true, "OOMScoreAdjust": true, "LimitNOFILE": true, "LimitNPROC": true,
	"PIDFile": true, "GuessMainPID": true, "NotifyAccess": true,
}

// importSystemdUnit maps one parsed unit onto a RunWisp task or service, opening
// a report row before anything about the mapping can go wrong.
func importSystemdUnit(names *namer, sections []systemdSection, unitName, source string) {
	svc := findSection(sections, "Service")
	if svc == nil {
		svc = &systemdSection{} // still open a row so the empty unit is reported
	}

	kind := systemdKind(svc)
	execStarts := svc.all("ExecStart")
	runLine, _ := systemdRunLine(execStarts)

	base := finalizeTaskName(unitName, "service")
	ref, name, skip := names.resolve(source, base, kind, runLine, 0)
	if skip {
		return
	}
	if unitName == "" {
		ref.note(NoteSystemdNoName,
			"read from stdin, so there's no filename to name this after — imported as \""+name+"\"; rename it before running.")
	}

	prefix, schedule := "services.", "service"
	if kind == model.KindTask {
		prefix, schedule = "tasks.", "@reboot"
	}
	b := block{header: prefix + name}

	if unit := findSection(sections, "Unit"); unit != nil {
		if desc := strings.TrimSpace(unit.last("Description")); desc != "" {
			b.set("description", tomlString(desc))
		}
	}

	run := systemdApplyRun(&b, svc, ref, execStarts)
	if kind == model.KindTask {
		b.set("run_on_start", "true")
		ref.note(NoteSystemdOneshot,
			"Type=oneshot → imported as a run-once task (run_on_start).")
	}
	env := systemdApplyServiceKeys(&b, svc, ref, kind)

	blocks := []block{b}
	if eb, ok := envBlock(prefix+name+".env", env); ok {
		blocks = append(blocks, eb)
	}
	ref.emit(name, kind, schedule, run, blocks...)
}

// systemdKind decides whether a unit maps onto a RunWisp service or a run-once
// task. Type=oneshot is the run-once task; everything else is a long-running
// service. Restart is irrelevant to the choice — RunWisp services always
// restart, so a Restart=no service becomes a service with a behavior note, not a
// task.
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
// or failure semantics RunWisp doesn't reproduce.
func stripExecPrefixes(cmd string) (string, bool) {
	trimmed := strings.TrimLeft(cmd, "@-+!:")
	trimmed = strings.TrimSpace(trimmed)
	return trimmed, trimmed != strings.TrimSpace(cmd)
}

// systemdApplyRun sets the run line from ExecStart, flagging the cases RunWisp
// can't carry over: no command, more than one command, and unfilled template
// specifiers. Returns the command that will run.
func systemdApplyRun(b *block, svc *systemdSection, ref itemRef, execStarts []string) string {
	extraExec := len(svc.all("ExecStartPre")) + len(svc.all("ExecStartPost")) +
		len(svc.all("ExecStop")) + len(svc.all("ExecStopPost")) + len(svc.all("ExecReload"))

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
	if strings.Contains(run, "%") {
		ref.note(NoteSystemdTemplate,
			"ExecStart uses systemd specifiers (%i, %n, %h …) RunWisp doesn't fill in. "+
				"Replace them with concrete values in the run line.")
	}
	b.set("run", tomlVerbatimString(run))

	if len(execStarts) > 1 || extraExec > 0 {
		ref.note(NoteSystemdMultiExec,
			"the unit ran more than one command (multiple ExecStart, or ExecStartPre/"+
				"Post/ExecStop) — RunWisp runs a single command, so only the first "+
				"ExecStart was imported. Fold the rest into the run script by hand.")
	}
	return run
}

// systemdApplyServiceKeys maps the [Service] directives RunWisp understands onto
// b and returns the parsed environment (nil when the unit set none). Anything it
// can't model becomes a note so nothing is silently dropped.
func systemdApplyServiceKeys(b *block, svc *systemdSection, ref itemRef, kind model.TaskKind) map[string]string {
	env := map[string]string{}
	var dropped []string
	var sawSandbox, sawSocket bool

	for _, kv := range svc.kvs {
		switch kv.key {
		case "ExecStart", "ExecStartPre", "ExecStartPost", "ExecStop", "ExecStopPost", "ExecReload", "Type":
			// handled in systemdApplyRun / systemdKind
		case "WorkingDirectory":
			b.set("working_dir", tomlString(kv.value))
			if !filepath.IsAbs(kv.value) {
				ref.note(NoteRelativeDirectory,
					"WorkingDirectory="+kv.value+" is relative; RunWisp resolves working_dir "+
						"against the runwisp.toml location.")
			}
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
			if d, ok := systemdSeconds(kv.value); ok {
				b.set("graceful_stop", tomlString(d))
			} else {
				ref.note(NoteKeyUnreadable,
					kv.key+"="+kv.value+" isn't a duration RunWisp can read, so it was dropped.")
			}
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

	systemdApplyUser(b, svc)
	systemdApplyEnvFiles(b, svc, ref)
	systemdApplyType(svc, ref)
	systemdApplyRestartBehavior(svc, ref, kind)
	systemdNoteDropped(ref, dropped, sawSandbox, sawSocket)
	return env
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
	canonical, ok := normalizeSignal(value)
	if !ok {
		ref.note(NoteKeyUnreadable, key+"="+value+" isn't a signal RunWisp can read, so it was dropped.")
		return
	}
	b.set("stop_signal", tomlString(canonical))
}

func systemdApplyUser(b *block, svc *systemdSection) {
	user := strings.TrimSpace(svc.last("User"))
	if user == "" {
		return
	}
	if group := strings.TrimSpace(svc.last("Group")); group != "" {
		user += ":" + group
	}
	b.set("user", tomlString(user))
}

func systemdApplyEnvFiles(b *block, svc *systemdSection, ref itemRef) {
	files := svc.all("EnvironmentFile")
	if len(files) == 0 {
		return
	}
	// A leading "-" marks the file optional in systemd; RunWisp always tolerates a
	// missing env_file, so the marker carries no meaning and is stripped.
	first := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(files[0]), "-"))
	b.set("env_file", tomlString(first))
	if len(files) > 1 {
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

// systemdApplyRestartBehavior notes when a service that systemd did not
// auto-restart becomes a RunWisp service, which always restarts.
func systemdApplyRestartBehavior(svc *systemdSection, ref itemRef, kind model.TaskKind) {
	if kind != model.KindService {
		return
	}
	restart := strings.ToLower(strings.TrimSpace(svc.last("Restart")))
	if restart == "" || restart == "no" {
		ref.note(NoteSystemdRestartBehavior,
			"the unit did not auto-restart (Restart="+orNone(restart)+"), but RunWisp services "+
				"always restart on exit. If it's meant to run once, set Type=oneshot in the source "+
				"or make it a [tasks.*] entry.")
	}
}

func orNone(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

// systemdSeconds turns a systemd time value into a RunWisp duration. It handles
// the common forms — a bare integer (seconds) and a "s"/"sec"/"min"/"m" suffix —
// and returns false for anything more exotic (compound spans, "infinity").
func systemdSeconds(value string) (string, bool) {
	v := strings.TrimSpace(value)
	if v == "" || strings.EqualFold(v, "infinity") {
		return "", false
	}
	if n, err := strconv.Atoi(v); err == nil {
		return strconv.Itoa(n) + "s", true
	}
	for _, suffix := range []struct{ systemd, runwisp string }{
		{"sec", "s"}, {"s", "s"}, {"min", "m"}, {"m", "m"}, {"h", "h"},
	} {
		if num, ok := strings.CutSuffix(v, suffix.systemd); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(num)); err == nil {
				return strconv.Itoa(n) + suffix.runwisp, true
			}
		}
	}
	return "", false
}

// parseSystemdEnvInto parses one Environment= value — space-separated KEY=VALUE
// assignments, values optionally quoted — into env. Multiple Environment= lines
// accumulate because parseSystemdUnit preserves them and this merges each in.
//
// ponytail: handles the common quoting (a whole assignment or its value wrapped
// in " or '); systemd's full C-escape grammar (\n, \x41, nested quotes) isn't
// reproduced — a value that relies on it lands verbatim for the operator to fix.
func parseSystemdEnvInto(env map[string]string, value string) {
	for _, tok := range splitSystemdEnv(value) {
		tok = unquoteSystemd(tok) // handles Environment="VAR=value with spaces"
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(tok[:eq])
		val := strings.TrimSpace(tok[eq+1:])
		val = unquoteSystemd(val)
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

// unquoteSystemd strips a single matching pair of surrounding quotes.
func unquoteSystemd(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}
