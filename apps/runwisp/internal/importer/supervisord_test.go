// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
)

func parseSup(t *testing.T, in string) *Result {
	t.Helper()
	return parseSupWith(t, in, SupervisordOptions{})
}

func parseSupWith(t *testing.T, in string, opts SupervisordOptions) *Result {
	t.Helper()
	res, err := ParseSupervisordReader(strings.NewReader(in), opts)
	if err != nil {
		t.Fatalf("ParseSupervisordReader: %v", err)
	}
	return res
}

func TestSupervisordBasicProgram(t *testing.T) {
	in := `[program:web]
command=/usr/bin/gunicorn app:app
directory=/srv/app
user=www-data
`
	res := parseSup(t, in)
	out := res.TOML()
	mustContain(t, out, "[services.web]")
	mustContain(t, out, `run = "/usr/bin/gunicorn app:app"`)
	mustContain(t, out, `working_dir = "/srv/app"`)
	mustContain(t, out, `user = "www-data"`)

	if tally := res.Tally(); tally.Tasks != 0 || tally.Services != 1 {
		t.Fatalf("counts: got %+v, want 0 tasks / 1 service", tally)
	}
}

func TestSupervisordAutorestartService(t *testing.T) {
	// true and unexpected (and an omitted value) are always-on services; a
	// service is always-restart, so no restart key is emitted.
	for _, value := range []string{"true", "unexpected"} {
		res := parseSup(t, "[program:x]\ncommand=/bin/x\nautorestart="+value+"\n")
		out := res.TOML()
		mustContain(t, out, "[services.x]")
		mustNotContain(t, out, "restart =")
		if tally := res.Tally(); tally.Tasks != 0 || tally.Services != 1 {
			t.Fatalf("%s: counts %+v, want 0 tasks / 1 service", value, tally)
		}
	}
}

func TestSupervisordAutorestartFalseIsTask(t *testing.T) {
	res := parseSup(t, "[program:x]\ncommand=/bin/x\nautorestart=false\n")
	out := res.TOML()
	mustContain(t, out, "[tasks.x]")
	mustContain(t, out, "run_on_start = true")
	mustContain(t, out, `restart = "never"`)
	if tally := res.Tally(); tally.Tasks != 1 || tally.Services != 0 {
		t.Fatalf("counts %+v, want 1 task / 0 services", tally)
	}
}

func TestSupervisordNumprocs(t *testing.T) {
	res := parseSup(t, "[program:worker]\ncommand=/bin/worker\nnumprocs=4\n")
	out := res.TOML()
	mustContain(t, out, "instances = 4")
	if !hasNoteKind(res, NoteInstances) {
		t.Fatalf("expected instance-index note, got %+v", allNotes(res))
	}
}

func TestSupervisordDurationsAndRetries(t *testing.T) {
	in := `[program:api]
command=/bin/api
startsecs=10
startretries=5
stopwaitsecs=30
stopsignal=INT
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, `healthy_after = "10s"`)
	mustContain(t, out, "restart_attempts = 5")
	mustContain(t, out, `graceful_stop = "30s"`)
	mustContain(t, out, `stop_signal = "SIGINT"`)
}

func TestSupervisordExitCodes(t *testing.T) {
	out := parseSup(t, "[program:x]\ncommand=/bin/x\nexitcodes=0,2\n").TOML()
	mustContain(t, out, "exit_codes = [0, 2]")
}

func TestSupervisordEnvironment(t *testing.T) {
	in := `[program:x]
command=/bin/x
environment=KEY1="value one",KEY2=plain,PARTS="a,b,c"
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, "[services.x.env]")
	mustContain(t, out, `KEY1 = "value one"`)
	mustContain(t, out, `KEY2 = "plain"`)
	mustContain(t, out, `PARTS = "a,b,c"`)
}

func TestSupervisordProgramName(t *testing.T) {
	out := parseSup(t, "[program:web]\ncommand=/bin/run --name %(program_name)s\n").TOML()
	mustContain(t, out, `run = "/bin/run --name web"`)
}

func TestSupervisordUnresolvedExpansion(t *testing.T) {
	res := parseSup(t, "[program:web]\ncommand=/bin/run --idx %(process_num)s\n")
	if !hasBlockingNote(res, "%(...)s") {
		t.Fatalf("expected unresolved-expansion note, got %+v", allNotes(res))
	}
	// The run line is syntactically fine, so the config loads — and then runs the
	// wrong command. That's scarier than a refusal, so the row still reads blocked.
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("status = %v, want blocked", got)
	}
}

func TestSupervisordGroup(t *testing.T) {
	in := `[group:site]
programs=web,worker

[program:web]
command=/bin/web

[program:worker]
command=/bin/worker
`
	out := parseSup(t, in).TOML()
	mustContain(t, out, `group = "site"`)
}

func TestSupervisordLogFilesDropped(t *testing.T) {
	in := `[program:x]
command=/bin/x
stdout_logfile=/var/log/x.log
stderr_logfile=/var/log/x.err
`
	res := parseSup(t, in)
	mustNotContain(t, res.TOML(), "/var/log/x.log")
	if !hasNoteKind(res, NoteLogsDropped) {
		t.Fatalf("expected log-dropped note, got %+v", allNotes(res))
	}
	// One note however many log keys appeared, and the row reads "changed" —
	// dropping the operator's log config is a difference, not a clean import.
	if got := len(res.Items()[0].Notes); got != 1 {
		t.Fatalf("expected exactly one log note, got %d", got)
	}
	if got := res.Items()[0].Status(); got != StatusChanged {
		t.Fatalf("status = %v, want changed", got)
	}
}

func TestSupervisordSkipsDaemonSections(t *testing.T) {
	in := `[unix_http_server]
file=/tmp/supervisor.sock

[supervisord]
logfile=/tmp/supervisord.log

[program:x]
command=/bin/x
`
	res := parseSup(t, in)
	if tally := res.Tally(); tally.Tasks != 0 || tally.Services != 1 {
		t.Fatalf("counts: got %+v, want 0 tasks / 1 service", tally)
	}
}

// TestUnsupportedProgramIsBlockedNotSkipped pins that blocking outranks skipped.
// An [eventlistener] emits no TOML, exactly like a program the live config
// already owns — but the operator's work is "go reimplement this", not "nothing
// to do". Collapsing the two would file the case that needs a human under the
// mark that means it doesn't.
func TestUnsupportedProgramIsBlockedNotSkipped(t *testing.T) {
	res := parseSup(t, "[eventlistener:memmon]\ncommand=memmon -a 200MB\n")
	if !hasBlockingNote(res, "isn't supported") {
		t.Fatalf("expected eventlistener note, got %+v", allNotes(res))
	}
	items := res.Items()
	if len(items) != 1 {
		t.Fatalf("an eventlistener is a process the operator runs, so it gets a row: %+v", items)
	}
	if items[0].Status() != StatusBlocked {
		t.Fatalf("status = %v, want blocked", items[0].Status())
	}
	if items[0].Source != "eventlistener:memmon" || items[0].Name != "" {
		t.Fatalf("row should name the section and emit nothing: %+v", items[0])
	}
}

func TestSupervisordIncludeFromStdinNoted(t *testing.T) {
	res := parseSup(t, "[include]\nfiles=conf.d/*.conf\n")
	if !hasBlockingNote(res, "stdin") {
		t.Fatalf("expected stdin-include note, got %+v", allNotes(res))
	}
}

func TestSupervisordContinuationLines(t *testing.T) {
	in := "[program:x]\ncommand=/bin/x\nenvironment=A=\"1\",\n    B=\"2\"\n"
	out := parseSup(t, in).TOML()
	mustContain(t, out, `A = "1"`)
	mustContain(t, out, `B = "2"`)
}

func TestSupervisordExistingSkipsSameProgram(t *testing.T) {
	// A program already promoted into the root TOML — same name, same kind, same
	// command — is skipped on re-import rather than emitted a second time, which
	// would fail the merged load. The cron path has always done this; supervisord
	// must too, or `import supervisord --write` breaks after a promote.
	opts := SupervisordOptions{Existing: Owned{"web": {Kind: model.KindService, Run: "/usr/bin/web --serve"}}}
	res := parseSupWith(t, "[program:web]\ncommand=/usr/bin/web --serve\n", opts)
	if services := res.Tally().Services; services != 0 {
		t.Fatalf("expected the matching program to be skipped, got %d services", services)
	}
	mustNotContain(t, res.TOML(), "[services.web]")
	if !hasNoteKind(res, NoteAlreadyDefined) {
		t.Fatalf("expected a skip note, got %+v", allNotes(res))
	}
	// A skipped program still gets a row — it used to disappear from the report.
	items := res.Items()
	if len(items) != 1 || items[0].Status() != StatusSkipped {
		t.Fatalf("expected one skipped row, got %+v", items)
	}
}

func TestSupervisordExistingRenamesDifferentCommand(t *testing.T) {
	opts := SupervisordOptions{Existing: Owned{"web": {Kind: model.KindService, Run: "/opt/other"}}}
	res := parseSupWith(t, "[program:web]\ncommand=/usr/bin/web --serve\n", opts)
	out := res.TOML()
	mustNotContain(t, out, "[services.web]")
	mustContain(t, out, "[services.web-2]")
	if !hasNoteKind(res, NoteRenamedOwned) {
		t.Fatalf("expected a rename note, got %+v", allNotes(res))
	}
}

func TestSupervisordExistingTaskDoesNotMatchService(t *testing.T) {
	// autorestart is unset, so this program imports as a service. An existing
	// *task* of the same name and command is a different thing — rename, not skip.
	opts := SupervisordOptions{Existing: Owned{"web": {Kind: model.KindTask, Run: "/usr/bin/web --serve"}}}
	res := parseSupWith(t, "[program:web]\ncommand=/usr/bin/web --serve\n", opts)
	mustContain(t, res.TOML(), "[services.web-2]")
}

// TestSupervisordSkipEmitsNoOtherNotes makes sure a skipped program doesn't leave
// behind commentary about a task that was never imported.
func TestSupervisordSkipEmitsNoOtherNotes(t *testing.T) {
	opts := SupervisordOptions{Existing: Owned{"web": {Kind: model.KindService, Run: "/usr/bin/web"}}}
	res := parseSupWith(t, "[program:web]\ncommand=/usr/bin/web\nautorestart=unexpected\n", opts)
	if hasNoteKind(res, NoteAutorestartUnexpected) {
		t.Fatalf("a skipped program must not explain a mapping that never happened: %+v", allNotes(res))
	}
}
