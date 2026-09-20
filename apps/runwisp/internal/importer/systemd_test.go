// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

func parseUnit(t *testing.T, in string) *Result {
	t.Helper()
	res, err := ParseSystemdReader(strings.NewReader(in), SystemdOptions{})
	if err != nil {
		t.Fatalf("ParseSystemdReader: %v", err)
	}
	return res
}

func TestSystemdBasicService(t *testing.T) {
	res := parseUnit(t, `[Unit]
Description=My API

[Service]
ExecStart=/usr/local/bin/bun run start
WorkingDirectory=/srv/app
User=deploy
Group=deploy
Restart=always
`)
	out := res.TOML()
	// Piped in, so the name falls back to a placeholder with a note.
	mustContain(t, out, "[services.service]")
	mustContain(t, out, `description = "My API"`)
	mustContain(t, out, `run = "/usr/local/bin/bun run start"`)
	mustContain(t, out, `working_dir = "/srv/app"`)
	mustContain(t, out, `user = "deploy:deploy"`)
	assertNotes(t, res, []NoteKind{NoteSystemdNoName})
}

func TestSystemdOneshotBecomesTask(t *testing.T) {
	res := parseUnit(t, `[Service]
Type=oneshot
ExecStart=/usr/local/bin/migrate up
`)
	out := res.TOML()
	mustContain(t, out, "[tasks.service]")
	mustContain(t, out, "run_on_start = true")
	if tally := res.Tally(); tally.Tasks != 1 || tally.Services != 0 {
		t.Fatalf("counts: got %+v, want 1 task / 0 services", tally)
	}
}

// TestSystemdAccumulatesEnvironment proves the systemd reader keeps every
// Environment= line instead of collapsing to the last, the way supervisord's INI
// reader would.
func TestSystemdAccumulatesEnvironment(t *testing.T) {
	res := parseUnit(t, `[Service]
ExecStart=/bin/app
Environment=NODE_ENV=production
Environment=PORT=3000 HOST="0.0.0.0"
`)
	out := res.TOML()
	mustContain(t, out, `NODE_ENV = "production"`)
	mustContain(t, out, `PORT = "3000"`)
	mustContain(t, out, `HOST = "0.0.0.0"`)
}

// TestSystemdEnvironmentWholeAssignmentQuoted proves the whole-assignment quoting
// form — Environment="VAR=value with spaces" — is unwrapped before the KEY=VALUE
// split, not left with a stray leading/trailing quote baked into the key/value.
func TestSystemdEnvironmentWholeAssignmentQuoted(t *testing.T) {
	res := parseUnit(t, `[Service]
ExecStart=/bin/app
Environment="NODE_OPTS=--max-old-space 512"
`)
	out := res.TOML()
	mustContain(t, out, `NODE_OPTS = "--max-old-space 512"`)
}

// TestSystemdKillSignalOutOfAllowlistIsNoted proves that a KillSignal= naming
// a signal outside model.StopSignals (RunWisp's 7-signal allowlist) either
// gets flagged with a note or isn't written as an unvalidated stop_signal
// value — never both silent and invalid, since config.Load's
// validateStopSignal would reject it anyway.
func TestSystemdKillSignalOutOfAllowlistIsNoted(t *testing.T) {
	res := parseUnit(t, `[Service]
ExecStart=/bin/app
KillSignal=SIGCONT
`)
	out := res.TOML()
	if strings.Contains(out, `stop_signal = "SIGCONT"`) && !hasNoteKind(res, NoteKeyUnreadable) {
		t.Fatalf("KillSignal=SIGCONT written as %q with no note, got notes %+v", "SIGCONT", allNotes(res))
	}
}

// TestSystemdMultiExecIsBlocking proves that a unit running more than one command
// keeps only the first ExecStart and flags the rest rather than silently dropping
// them.
func TestSystemdMultiExecIsBlocking(t *testing.T) {
	res := parseUnit(t, `[Service]
ExecStartPre=/bin/setup
ExecStart=/bin/main
ExecStart=/bin/second
`)
	out := res.TOML()
	mustContain(t, out, `run = "/bin/main"`)
	mustNotContain(t, out, "/bin/second")
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("status: got %v, want blocked", got)
	}
}

// systemdGoldenCases pins the whole emitted TOML and report for representative
// units read via the single-file (filename-named) path.
var systemdGoldenCases = []struct {
	name        string
	file        string
	expectNotes []NoteKind
}{
	// The happy path: the exact shape a bun/node service deploy produces. Every
	// directive maps or is cosmetic, so nothing is left for a human.
	{name: "full", file: "testdata/systemd/full.service"},
	// A Type=oneshot unit becomes a run-once task.
	{name: "oneshot", file: "testdata/systemd/oneshot.service", expectNotes: []NoteKind{
		NoteSystemdOneshot,
	}},
	// Everything RunWisp can't carry over, surfaced as notes: notify readiness,
	// multiple ExecStart, a template specifier, a relative WorkingDirectory, two
	// EnvironmentFiles, sandboxing, socket activation, an unknown directive, and
	// the always-restart behavior change.
	{name: "messy", file: "testdata/systemd/messy.service", expectNotes: []NoteKind{
		NoteSystemdType, NoteSystemdMultiExec, NoteSystemdTemplate, NoteRelativeDirectory,
		NoteSystemdEnvFileMulti, NoteSystemdSandbox, NoteSystemdSocketActivation,
		NoteSystemdRestartBehavior, NoteKeysUnsupported,
	}},
}

func TestSystemdGolden(t *testing.T) {
	for _, tc := range systemdGoldenCases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseSystemdFiles([]string{tc.file}, SystemdOptions{})
			if err != nil {
				t.Fatalf("ParseSystemdFiles: %v", err)
			}
			assertReportAccountsForTOML(t, res)
			checkGolden(t, goldenPath(tc.file), res.TOML())
			checkReportGolden(t, reportGoldenPath(tc.file), res)
			assertNotes(t, res, tc.expectNotes)
		})
	}
}

// TestSystemdContinuationLine proves a backslash-continued directive is read as
// one logical line.
func TestSystemdContinuationLine(t *testing.T) {
	res := parseUnit(t, `[Service]
ExecStart=/bin/app \
  --flag one \
  --flag two
`)
	mustContain(t, res.TOML(), `run = "/bin/app --flag one --flag two"`)
}
