// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"os"
	"path/filepath"
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
	mustContain(t, out, `run_on_start = "boot"`)
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
	// A timer and the service it starts become one scheduled task. Persistent
	// keeps the default catch_up; RandomizedDelaySec lands on jitter.
	{name: "timer", file: "testdata/systemd/backup.timer", expectNotes: []NoteKind{
		NoteSystemdTimerApprox,
	}},
	// A monotonic timer: OnUnitActiveSec is an @every interval, OnBootSec a
	// run once per boot. Unit= names a service that isn't called after the timer.
	{name: "monotonic-timer", file: "testdata/systemd/poll.timer", expectNotes: []NoteKind{
		NoteSystemdTimerApprox,
	}},
	// A timer whose service file is missing, with two OnCalendar= lines.
	{name: "orphan-timer", file: "testdata/systemd/orphan.timer", expectNotes: []NoteKind{
		NoteSystemdTimerNoService, NoteSystemdTimerMultiCalendar,
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

// TestSystemdRestartMapsToRestartPolicy: a unit that systemd restarts only on
// failure, or never, used to become an always-restart service.
func TestSystemdRestartMapsToRestartPolicy(t *testing.T) {
	cases := []struct {
		restart, want string
		noted         bool
	}{
		{"always", "", false},
		{"on-failure", `restart = "on_failure"`, false},
		{"no", `restart = "never"`, false},
		{"", `restart = "never"`, false},
		{"on-success", `restart = "never"`, true},
		{"on-abnormal", `restart = "on_failure"`, true},
		{"on-abort", `restart = "on_failure"`, true},
	}
	for _, tc := range cases {
		in := "[Service]\nExecStart=/bin/app\n"
		if tc.restart != "" {
			in += "Restart=" + tc.restart + "\n"
		}
		res := parseUnit(t, in)
		out := res.TOML()
		if tc.want == "" {
			mustNotContain(t, out, "restart =")
		} else {
			mustContain(t, out, tc.want)
		}
		if got := hasNoteKind(res, NoteSystemdRestartBehavior); got != tc.noted {
			t.Errorf("Restart=%q: note present = %v, want %v", tc.restart, got, tc.noted)
		}
	}
}

// TestSystemdZeroTimeoutIsNotImportedAsInstantKill: in systemd 0 means wait
// forever, in RunWisp "0s" means SIGKILL at once.
func TestSystemdZeroTimeoutIsNotImportedAsInstantKill(t *testing.T) {
	for _, line := range []string{"TimeoutStopSec=0", "TimeoutSec=0", "TimeoutStopSec=0s", "TimeoutStopSec=0min"} {
		res := parseUnit(t, "[Service]\nExecStart=/bin/app\n"+line+"\n")
		mustNotContain(t, res.TOML(), "graceful_stop")
		if !hasNoteKind(res, NoteKeyUnreadable) {
			t.Errorf("%s: dropped without a note, got %+v", line, allNotes(res))
		}
	}
	mustContain(t, parseUnit(t, "[Service]\nExecStart=/bin/app\nTimeoutStopSec=30\n").TOML(), `graceful_stop = "30s"`)
}

// TestSystemdLastStopTimeoutWins: TimeoutSec and TimeoutStopSec both set the
// stop timeout and the last one wins, so the import writes one key (two would
// be a TOML error) and a later 0 clears an earlier value.
func TestSystemdLastStopTimeoutWins(t *testing.T) {
	out := parseUnit(t, "[Service]\nExecStart=/bin/app\nTimeoutSec=20\nTimeoutStopSec=10\n").TOML()
	if n := strings.Count(out, "graceful_stop"); n != 1 {
		t.Fatalf("graceful_stop written %d times:\n%s", n, out)
	}
	mustContain(t, out, `graceful_stop = "10s"`)
	mustNotContain(t, parseUnit(t, "[Service]\nExecStart=/bin/app\nTimeoutStopSec=10\nTimeoutStopSec=0\n").TOML(), "graceful_stop")
}

// TestSystemdEscapedPercentIsNotASpecifier: %% is a literal % to systemd, so it
// is resolved on import and doesn't ask the operator to fill anything in.
func TestSystemdEscapedPercentIsNotASpecifier(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/date +%%Y\n")
	mustContain(t, res.TOML(), `run = "/bin/date +%Y"`)
	if hasNoteKind(res, NoteSystemdTemplate) {
		t.Errorf("%%%% flagged as a specifier: %+v", allNotes(res))
	}
	if !hasNoteKind(parseUnit(t, "[Service]\nExecStart=/bin/app %i\n"), NoteSystemdTemplate) {
		t.Error("%i not flagged as a specifier")
	}
}

// TestSystemdCommentDoesNotContinue: a comment line ending in a backslash is
// still just a comment; it must not swallow the directive after it.
func TestSystemdCommentDoesNotContinue(t *testing.T) {
	res := parseUnit(t, "[Service]\n# old flags \\\nExecStart=/bin/app\n")
	mustContain(t, res.TOML(), `run = "/bin/app"`)
}

// TestSystemdOptionalEnvironmentFileMustExist: RunWisp's env_file has to exist,
// so EnvironmentFile=-path is imported only when the file is there.
func TestSystemdOptionalEnvironmentFileMustExist(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.env")
	if err := os.WriteFile(present, []byte("A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.env")

	res := parseUnit(t, "[Service]\nExecStart=/bin/app\nEnvironmentFile=-"+missing+"\n")
	mustNotContain(t, res.TOML(), "env_file")
	assertNotes(t, res, []NoteKind{NoteSystemdNoName, NoteSystemdEnvFileMissing})

	res = parseUnit(t, "[Service]\nExecStart=/bin/app\nEnvironmentFile=-"+present+"\n")
	mustContain(t, res.TOML(), `env_file = "`+present+`"`)
	assertNotes(t, res, []NoteKind{NoteSystemdNoName})

	// A missing optional file is skipped in favour of the next usable one.
	res = parseUnit(t, "[Service]\nExecStart=/bin/app\nEnvironmentFile=-"+missing+"\nEnvironmentFile="+present+"\n")
	mustContain(t, res.TOML(), `env_file = "`+present+`"`)
	if hasNoteKind(res, NoteSystemdEnvFileMulti) {
		t.Errorf("one usable file should not trigger the multi-file note: %+v", allNotes(res))
	}
}

// TestSystemdExecStartShellMetacharactersAreQuoted: systemd execs ExecStart
// without a shell, but RunWisp runs `run` through sh -c, so each argument must
// reach the program exactly as systemd would pass it: no globbing, no command
// chaining or substitution, and only the variable expansion systemd itself does.
func TestSystemdExecStartShellMetacharactersAreQuoted(t *testing.T) {
	cases := map[string]string{
		`/bin/app --glob *.log`:        `/bin/app --glob '*.log'`,
		`/bin/app a;b`:                 `/bin/app 'a;b'`,
		`/bin/app "a b;c" --y`:         `/bin/app 'a b;c' --y`,
		`/bin/app 'x*' $(id)`:          `/bin/app 'x*' '$(id)'`,
		`/bin/a ; /bin/b`:              `/bin/a ; /bin/b`,
		`/bin/app | tee`:               `/bin/app '|' tee`,
		`-/bin/app plain`:              `/bin/app plain`,
		`/bin/app   spaced   args`:     `/bin/app spaced args`,
		`/bin/sh -c "echo \" x* done"`: `/bin/sh -c 'echo " x* done'`,
		"/bin/echo \"`id -u`\"":        "/bin/echo '`id -u`'",
		`/bin/echo "a"*`:               `/bin/echo 'a*'`,
		`/bin/echo 'it\'s'`:            `/bin/echo 'it'\''s'`,
		`/bin/echo ""`:                 `/bin/echo ''`,
		// Unknown escapes keep their backslash; `\;` alone is a literal `;`.
		`/bin/app --name=a\;b`: `/bin/app '--name=a\;b'`,
		`/bin/echo foo\\|bar`:  `/bin/echo 'foo\|bar'`,
		`/bin/echo x \; ";"`:   `/bin/echo x ';' ';'`,
		// ${VAR} expands anywhere (even in single quotes) as one argument; $VAR
		// only as a whole word; $$ and %% are a literal $ and %.
		`/bin/x --port ${PORT} --a=$HOME/${X}`: `/bin/x --port "${PORT}" '--a=$HOME/'"${X}"`,
		`/bin/app a=${X}*`:                     `/bin/app a="${X}"'*'`,
		`/bin/echo $FOO '${FOO}'`:              `/bin/echo $FOO "${FOO}"`,
		`/usr/bin/printf [%%s] $$HOME a$$b`:    `/usr/bin/printf '[%s]' '$HOME' 'a$b'`,
		`/bin/date +%%Y %i`:                    `/bin/date +%Y %i`,
	}
	for in, want := range cases {
		got, _ := stripExecPrefixes(in)
		if got != want {
			t.Errorf("stripExecPrefixes(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSystemdCommentInsideContinuationIsSkipped: systemd drops a comment line
// even in the middle of a continued directive (systemd.syntax(7)), so a flag
// commented out of a multi-line ExecStart stays out of the imported command.
func TestSystemdCommentInsideContinuationIsSkipped(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/app \\\n  --port=3000 \\\n#  --inspect \\\n;  --trace \\\n  --verbose\n")
	mustContain(t, res.TOML(), `run = "/bin/app --port=3000 --verbose"`)
}

// TestSystemdTimeSpan: systemd spells spans with days, weeks and spaces
// (systemd-tmpfiles-clean.timer uses OnUnitActiveSec=1d). Those were written as
// `@every 1d`, which doesn't load.
func TestSystemdTimeSpan(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"90", "1m30s", true},
		{"5sec", "5s", true},
		{"100min", "1h40m", true},
		{"1h", "1h", true},
		{"1d", "24h", true},
		{"1w", "168h", true},
		{"1h 30min", "1h30m", true},
		{"1d 12h", "36h", true},
		{"1.5h", "1h30m", true},
		{"2min30s", "2m30s", true},
		{"500ms", "500ms", true},
		{"0", "0s", true},
		{"infinity", "", false},
		{"", "", false},
		{"5 parsecs", "", false},
		{"-1s", "", false},
	}
	for _, tc := range cases {
		got, ok := systemdTimeSpan(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("systemdTimeSpan(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestSystemdIntervalInDays(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/clean\n[Timer]\nOnUnitActiveSec=1d\n")
	mustContain(t, res.TOML(), `cron = "@every 24h"`)
	if hasNoteKind(res, NoteCronUnparseable) {
		t.Fatal("1d is a span RunWisp can read")
	}
}

// TestSystemdMultiExecNamesTheRest: man-db.service's useful command is its
// second ExecStart. The note has to show it, or a human must open the unit to
// learn what wasn't imported.
func TestSystemdMultiExecNamesTheRest(t *testing.T) {
	res := parseUnit(t, "[Service]\nType=oneshot\nExecStart=+/usr/bin/install -d /var/cache/man\nExecStart=/usr/bin/mandb --quiet\n")
	if n := findNote(t, res, NoteSystemdMultiExec); !strings.Contains(n.Message, "ExecStart=/usr/bin/mandb --quiet") {
		t.Fatalf("note should name the other command: %s", n.Message)
	}
}

func TestSystemdOneshotStartTimeoutIsTheRunTimeout(t *testing.T) {
	res := parseUnit(t, "[Service]\nType=oneshot\nExecStart=/bin/job\nTimeoutStartSec=10min\n")
	mustContain(t, res.TOML(), `timeout = "10m"`)

	res = parseUnit(t, "[Service]\nExecStart=/bin/server\nTimeoutStartSec=10min\n")
	mustNotContain(t, res.TOML(), "timeout =")
}

// TestSystemdDirectivesThatAffectFailuresAreNoted: these were dropped without a
// word, though OnFailure= and SuccessExitStatus= decide what a failure looks like.
func TestSystemdDirectivesThatAffectFailuresAreNoted(t *testing.T) {
	res := parseUnit(t, `[Unit]
OnFailure=alert@%n.service
ConditionPathExists=/etc/letsencrypt
After=network.target

[Service]
Type=oneshot
ExecStart=/usr/bin/certbot renew
SuccessExitStatus=2
Nice=10
StandardOutput=append:/var/log/certrenew.log
`)
	var msgs []string
	for _, n := range res.Items()[0].Notes {
		msgs = append(msgs, n.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, want := range []string{"OnFailure=alert@%n.service", "ConditionPathExists=/etc/letsencrypt",
		"SuccessExitStatus", "Nice", "/var/log/certrenew.log"} {
		if !strings.Contains(all, want) {
			t.Errorf("no note mentions %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "After") {
		t.Errorf("ordering directives don't matter to a task:\n%s", all)
	}
}

func TestSystemdSpecifierNoteSaysWhatSystemdRan(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/sh -c 'date +%T; echo 100%%'\n")
	n := findNote(t, res, NoteSystemdTemplate)
	if !strings.Contains(n.Message, "%T (/tmp)") {
		t.Fatalf("note should say %%T is /tmp: %s", n.Message)
	}
}

func TestSystemdExtraCalendarsAreSpelledOut(t *testing.T) {
	res := parseUnit(t, "[Service]\nExecStart=/bin/sync\n[Timer]\nOnCalendar=*-*-* 04:00\nOnCalendar=Sat 12:00\n")
	n := findNote(t, res, NoteSystemdTimerMultiCalendar)
	if !strings.Contains(n.Message, `Sat 12:00 → cron = "0 12 * * 6"`) {
		t.Fatalf("note should give the other schedule as cron: %s", n.Message)
	}
}
