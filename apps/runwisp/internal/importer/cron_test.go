// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
)

func parseCron(t *testing.T, in string, opts CronOptions) *Result {
	t.Helper()
	res, err := ParseCrontab(strings.NewReader(in), opts)
	if err != nil {
		t.Fatalf("ParseCrontab: %v", err)
	}
	return res
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("expected output to contain %q\n--- output ---\n%s", needle, haystack)
	}
}

func mustNotContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Errorf("expected output NOT to contain %q\n--- output ---\n%s", needle, haystack)
	}
}

// allNotes flattens the report — the file-level notes plus every row's own —
// for assertions that care that something was explained, not where it was said.
func allNotes(r *Result) []Note {
	notes := append([]Note(nil), r.Notes()...)
	for _, it := range r.Items() {
		notes = append(notes, it.Notes...)
	}
	return notes
}

// hasBlockingNote reports whether anything in the report says, in words
// containing substr, that a human is needed.
func hasBlockingNote(r *Result, substr string) bool {
	for _, n := range allNotes(r) {
		if n.Blocking() && strings.Contains(n.Message, substr) {
			return true
		}
	}
	return false
}

// hasNoteKind asserts on a note's identity rather than its prose, so rewording
// a message isn't a test edit.
func hasNoteKind(r *Result, kind NoteKind) bool {
	for _, n := range allNotes(r) {
		if n.Kind == kind {
			return true
		}
	}
	return false
}

// findNote returns the one note of a kind, for the cases where the note's prose
// is the deliverable — a paste-able config block, say — and not just a marker.
func findNote(t *testing.T, r *Result, kind NoteKind) Note {
	t.Helper()
	for _, n := range allNotes(r) {
		if n.Kind == kind {
			return n
		}
	}
	t.Fatalf("no note of kind %q in the report: %+v", kind.Slug(), allNotes(r))
	return Note{}
}

func TestCronBasicFiveField(t *testing.T) {
	res := parseCron(t, "30 2 * * * /usr/local/bin/backup.sh --full\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, "[tasks.backup]")
	mustContain(t, out, `cron = "30 2 * * *"`)
	mustContain(t, out, `run = "/usr/local/bin/backup.sh --full"`)

	if tally := res.Tally(); tally.Tasks != 1 || tally.Services != 0 {
		t.Fatalf("counts: got %+v, want 1 task / 0 services", tally)
	}
	items := res.Items()
	if len(items) != 1 || items[0].Name != "backup" || items[0].Schedule != "30 2 * * *" {
		t.Fatalf("unexpected items: %+v", items)
	}
}

func TestCronReboot(t *testing.T) {
	res := parseCron(t, "@reboot /opt/app/warmup\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, "[tasks.warmup]")
	mustContain(t, out, "run_on_start = true")
	mustNotContain(t, out, "cron =")
	if res.Items()[0].Schedule != "@reboot" {
		t.Fatalf("schedule: got %q", res.Items()[0].Schedule)
	}
}

func TestCronDescriptors(t *testing.T) {
	res := parseCron(t, "@daily /bin/rotate\n@midnight /bin/nightly\n@annually /bin/yearly\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `cron = "@daily"`)
	mustContain(t, out, `cron = "@daily"`)  // @midnight normalized
	mustContain(t, out, `cron = "@yearly"`) // @annually normalized
}

func TestCronMailto(t *testing.T) {
	res := parseCron(t, "MAILTO=ops@example.com\n0 * * * * /bin/hourly\n", CronOptions{})
	if !hasBlockingNote(res, "MAILTO") {
		t.Fatalf("expected MAILTO note, got %+v", allNotes(res))
	}
	mustNotContain(t, res.TOML(), "MAILTO")
}

func TestCronShellAndEnv(t *testing.T) {
	in := "SHELL=/bin/bash\nPATH=/usr/local/bin:/usr/bin\nHOME=/home/deploy\n0 1 * * * /bin/job\n"
	res := parseCron(t, in, CronOptions{})
	out := res.TOML()
	// SHELL / env fold onto the task, not daemon-wide singletons, so the output
	// is safe to live in an included staging file.
	mustNotContain(t, out, "[defaults]")
	mustContain(t, out, "[tasks.job]")
	mustContain(t, out, `shell = "/bin/bash"`)
	mustContain(t, out, "[tasks.job.env]")
	mustContain(t, out, `PATH = "/usr/local/bin:/usr/bin"`)
	mustContain(t, out, `HOME = "/home/deploy"`)
}

func TestCronRelativeShellNoted(t *testing.T) {
	res := parseCron(t, "SHELL=bash\n0 1 * * * /bin/job\n", CronOptions{})
	if !hasBlockingNote(res, "absolute path") {
		t.Fatalf("expected relative-shell note, got %+v", allNotes(res))
	}
	mustNotContain(t, res.TOML(), "shell =")
}

func TestCronTimezone(t *testing.T) {
	res := parseCron(t, "CRON_TZ=Europe/Bratislava\n0 4 * * * /bin/job\n", CronOptions{})
	out := res.TOML()
	// CRON_TZ/TZ folds onto each task's timezone, not the [scheduler] singleton.
	mustNotContain(t, out, "[scheduler]")
	mustContain(t, out, "[tasks.job]")
	mustContain(t, out, `timezone = "Europe/Bratislava"`)
}

func TestCronExistingSkipsSameCommand(t *testing.T) {
	// A job whose derived name AND command already exist in the live config
	// (typically promoted into the root TOML) is skipped on re-import, with an
	// info note so the skip is never silent.
	opts := CronOptions{Existing: Owned{"backup": {Kind: model.KindTask, Run: "/usr/bin/backup.sh --full"}}}
	res := parseCron(t, "30 2 * * * /usr/bin/backup.sh --full\n", opts)
	if tasks := res.Tally().Tasks; tasks != 0 {
		t.Fatalf("expected the matching job to be skipped, got %d tasks", tasks)
	}
	mustNotContain(t, res.TOML(), "[tasks.backup]")
	if !hasNoteKind(res, NoteAlreadyDefined) {
		t.Fatalf("expected a skip note, got %+v", allNotes(res))
	}
	// The skip still gets a row: a job that vanishes from the report is a job
	// that was silently dropped, which is the whole point of the report.
	items := res.Items()
	if len(items) != 1 || items[0].Status() != StatusSkipped || items[0].Source != "backup" {
		t.Fatalf("expected one skipped row named backup, got %+v", items)
	}
}

func TestCronExistingRenamesDifferentCommand(t *testing.T) {
	// Same derived name but a DIFFERENT command is a genuine clash: the job is
	// preserved under name-2 rather than colliding with the existing task.
	opts := CronOptions{Existing: Owned{"backup": {Kind: model.KindTask, Run: "/opt/something-else"}}}
	res := parseCron(t, "30 2 * * * /usr/bin/backup.sh --full\n", opts)
	out := res.TOML()
	mustNotContain(t, out, "[tasks.backup]")
	mustContain(t, out, "[tasks.backup-2]")
	mustContain(t, out, `run = "/usr/bin/backup.sh --full"`)
	if !hasNoteKind(res, NoteRenamedOwned) {
		t.Fatalf("expected a rename note, got %+v", allNotes(res))
	}
	// Renamed is not the same as imported-as-written, so the row can't read clean.
	if got := res.Items()[0].Status(); got != StatusChanged {
		t.Fatalf("a renamed job is a difference, got %v", got)
	}
}

func TestCronExistingServiceForcesRename(t *testing.T) {
	// A clash against a service always renames, never skips — a service and a
	// task are different things even at the same name with the same command.
	opts := CronOptions{Existing: Owned{"worker": {Kind: model.KindService, Run: "/usr/bin/worker"}}}
	res := parseCron(t, "* * * * * /usr/bin/worker\n", opts)
	out := res.TOML()
	mustNotContain(t, out, "[tasks.worker]")
	mustContain(t, out, "[tasks.worker-2]")
}

func TestCronExistingCommandlessEntryForcesRename(t *testing.T) {
	// A compose-backed task has no comparable one-shot command. Two entries that
	// merely both lack a command are not the same job, so this renames.
	opts := CronOptions{Existing: Owned{"worker": {Kind: model.KindTask, Run: ""}}}
	res := parseCron(t, "* * * * * \n", opts)
	mustNotContain(t, res.TOML(), "[tasks.worker]")
}

func TestCronNameDedupe(t *testing.T) {
	in := "0 1 * * * /bin/job\n0 2 * * * /bin/job\n0 3 * * * /bin/job\n"
	res := parseCron(t, in, CronOptions{})
	out := res.TOML()
	mustContain(t, out, "[tasks.job]")
	mustContain(t, out, "[tasks.job-2]")
	mustContain(t, out, "[tasks.job-3]")
	// An in-import collision renamed two of the three jobs, and a rename is a
	// difference the operator has to know about — it used to happen in silence.
	var got []ItemStatus
	for _, it := range res.Items() {
		got = append(got, it.Status())
	}
	want := []ItemStatus{StatusClean, StatusChanged, StatusChanged}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("statuses: got %v, want %v", got, want)
	}
	if !hasNoteKind(res, NoteRenamedCollision) {
		t.Fatalf("expected an in-import collision note, got %+v", allNotes(res))
	}
}

// TestCronImportsCronsEnvironment: a job that worked under crond was written
// against crond's near-empty environment. Importing it without saying so hands
// it the daemon's instead, which is how a `✓ clean` import starts behaving
// differently the first time the daemon is launched from a different shell.
func TestCronImportsCronsEnvironment(t *testing.T) {
	res := parseCron(t, "0 4 * * * /bin/rollup\n", CronOptions{})
	mustContain(t, res.TOML(), `env_base = "clean"`)
	// It is fidelity, not a compromise, so it must not make the row read as a
	// difference the operator has to review.
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("status = %v, want clean", got)
	}
}

// TestCronPathLayersOverTheCleanBase is the pair to it: a crontab's own PATH is
// imported as task env, which the executor layers over the clean base. If the
// importer ever stopped emitting one, jobs would silently drop to /usr/bin:/bin.
func TestCronPathLayersOverTheCleanBase(t *testing.T) {
	res := parseCron(t, "PATH=/opt/bin:/usr/bin\n0 4 * * * /bin/rollup\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `env_base = "clean"`)
	mustContain(t, out, `PATH = "/opt/bin:/usr/bin"`)
	assertGeneratedConfigLoads(t, out)
}

// TestCronRunsInTheHomeDirectory: crond runs a job in the invoking user's home,
// RunWisp defaults to the daemon's working directory. A per-user crontab emits
// no `user`, so the task runs as the daemon's account and `~` is that same
// account's home — the rule holds without knowing who either of them is.
func TestCronRunsInTheHomeDirectory(t *testing.T) {
	res := parseCron(t, "0 4 * * * ./maintenance.sh\n", CronOptions{})
	mustContain(t, res.TOML(), `working_dir = "~"`)
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("status = %v, want clean", got)
	}
}

// TestCronSystemWorkingDirIsNotGuessed is the half that would be confidently
// wrong: a system crontab's job runs as its user column, but working_dir
// resolves once at config load against the *daemon's* home. Writing "~" would
// point deploy's job at root's home.
func TestCronSystemWorkingDirIsNotGuessed(t *testing.T) {
	res := parseCron(t, "0 5 * * * deploy ./cleanup.sh\n", CronOptions{System: true})
	out := res.TOML()
	mustContain(t, out, `user = "deploy"`)
	mustNotContain(t, out, "working_dir")
	if !hasNoteKind(res, NoteWorkingDirUser) {
		t.Fatalf("expected a working-dir note, got %+v", allNotes(res))
	}
	// It belongs to the file, not the job: every row of a system crontab has a
	// user column, so a per-row note would be the same sentence down the report.
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("status = %v, want clean", got)
	}
}

// TestCronSystemWithoutUserColumnsSaysNothing keeps the note honest: it fires on
// the thing it names, not on the --system flag.
func TestCronSystemWithoutUserColumnsSaysNothing(t *testing.T) {
	res := parseCron(t, "# nothing importable here\n", CronOptions{System: true})
	if hasNoteKind(res, NoteWorkingDirUser) {
		t.Fatalf("unexpected working-dir note, got %+v", allNotes(res))
	}
}

// TestCronMailtoHandsOverTheNotifier: "wire a notifier instead" reads as
// complete advice and isn't — a notifier needs a type, an id, a from, and a
// route, and the operator finds that out only after their cron mail has been
// off for a week. The note carries the block to paste.
func TestCronMailtoHandsOverTheNotifier(t *testing.T) {
	res := parseCron(t, "MAILTO=ops@example.com\n0 4 * * * /bin/rollup\n", CronOptions{})
	note := findNote(t, res, NoteMailto)

	for _, want := range []string{
		`type = "sendmail"`,          // the type that reuses the MTA cron already used
		`to   = ["ops@example.com"]`, // their address, not a placeholder
		"notify_on_failure",          // the half that actually routes it
	} {
		if !strings.Contains(note.Message, want) {
			t.Errorf("MAILTO note is missing %q:\n%s", want, note.Message)
		}
	}
	// cron mailed any output; RunWisp mails an event. Handing over a config
	// without saying that is how the difference gets discovered in production.
	if !strings.Contains(note.Message, "failures") {
		t.Errorf("MAILTO note doesn't name the behaviour difference:\n%s", note.Message)
	}
}

// TestCronEmptyMailtoIsNotAGapToFill: MAILTO= means "mail nobody", so proposing
// a mail notifier for it would invent a requirement the crontab disclaimed.
func TestCronEmptyMailtoIsNotAGapToFill(t *testing.T) {
	res := parseCron(t, "MAILTO=\"\"\n0 4 * * * /bin/rollup\n", CronOptions{})
	note := findNote(t, res, NoteMailto)
	if strings.Contains(note.Message, "sendmail") {
		t.Errorf("an empty MAILTO should not propose a notifier:\n%s", note.Message)
	}
}

func TestCronCommentBecomesDescription(t *testing.T) {
	in := "# Rotate the nginx logs\n0 0 * * * /usr/sbin/logrotate\n"
	res := parseCron(t, in, CronOptions{})
	mustContain(t, res.TOML(), `description = "Rotate the nginx logs"`)
}

func TestCronInvalidExpression(t *testing.T) {
	res := parseCron(t, "99 99 * * * /bin/bad\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, "TODO:")
	if !hasBlockingNote(res, "didn't parse") {
		t.Fatalf("expected parse-failure note, got %+v", allNotes(res))
	}
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("expected the row blocked, got %v", got)
	}
}

// TestCronBadTimezoneBlocksTheJob is the timezone-blind-validation bug: the cron
// expression was validated with an empty timezone even when the crontab set
// CRON_TZ, so a zone RunWisp can't load imported as a clean ✓ and then failed
// config.Load. The report must not call a job clean when it cannot run.
func TestCronBadTimezoneBlocksTheJob(t *testing.T) {
	res := parseCron(t, "CRON_TZ=Mars/Olympus\n0 4 * * * /bin/rollup\n", CronOptions{})
	out := res.TOML()
	// The TODO goes on the timezone line, not the cron line: the expression is
	// fine, the zone isn't, and two problems deserve two fixes.
	mustContain(t, out, `timezone = "Mars/Olympus"  # TODO`)
	mustContain(t, out, `cron = "0 4 * * *"`)
	mustNotContain(t, out, `cron = "0 4 * * *"  # TODO`)
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("status = %v, want blocked", got)
	}
	if !hasNoteKind(res, NoteTimezoneInvalid) {
		t.Fatalf("expected a timezone note, got %+v", allNotes(res))
	}
	assertGeneratedConfigDoesNotLoad(t, out)
}

// TestCronBadTimezoneBlocksARebootJob is the half a one-line fix would miss.
// @reboot consults no cron grammar, so validating the expression with the
// timezone attached would never look at the zone — but the job still runs under
// it.
func TestCronBadTimezoneBlocksARebootJob(t *testing.T) {
	res := parseCron(t, "CRON_TZ=Mars/Olympus\n@reboot /bin/warmup\n", CronOptions{})
	mustContain(t, res.TOML(), `timezone = "Mars/Olympus"  # TODO`)
	if got := res.Items()[0].Status(); got != StatusBlocked {
		t.Fatalf("status = %v, want blocked", got)
	}
}

// TestCronGoodTimezoneStaysClean is the other side of the same fix: a valid zone
// must not start reading as a problem.
func TestCronGoodTimezoneStaysClean(t *testing.T) {
	res := parseCron(t, "CRON_TZ=Europe/Bratislava\n0 4 * * * /bin/rollup\n", CronOptions{})
	mustNotContain(t, res.TOML(), "TODO")
	if got := res.Items()[0].Status(); got != StatusClean {
		t.Fatalf("status = %v, want clean", got)
	}
}

func TestCronSystemUserColumn(t *testing.T) {
	res := parseCron(t, "0 5 * * * deploy /usr/bin/cleanup\n", CronOptions{System: true})
	out := res.TOML()
	mustContain(t, out, `cron = "0 5 * * *"`)
	mustContain(t, out, `user = "deploy"`)
	mustContain(t, out, `run = "/usr/bin/cleanup"`)
}

func TestCronSystemUserColumnOnDescriptorLine(t *testing.T) {
	// Vixie cron allows `@reboot root /usr/bin/foo` in /etc/crontab. The user
	// column has to come off the descriptor form too, or it rides into `run`.
	res := parseCron(t, "@reboot root /usr/bin/warmup\n", CronOptions{System: true})
	out := res.TOML()
	mustContain(t, out, "run_on_start = true")
	mustContain(t, out, `user = "root"`)
	mustContain(t, out, `run = "/usr/bin/warmup"`)
	mustNotContain(t, out, `run = "root /usr/bin/warmup"`)
}

func TestCronSystemUserColumnOnLongDescriptorLine(t *testing.T) {
	// The other half of the same bug: the user used to be recovered by a second
	// six-field split of the line, so a descriptor job with enough arguments
	// handed a command argument to `user =` instead of dropping it.
	res := parseCron(t, "@reboot deploy /usr/bin/warmup --a --b --c --d\n", CronOptions{System: true})
	out := res.TOML()
	mustContain(t, out, `user = "deploy"`)
	mustContain(t, out, `run = "/usr/bin/warmup --a --b --c --d"`)
	mustNotContain(t, out, `user = "--b"`)
}

func TestCronDescriptorLinePerUserInventsNoUser(t *testing.T) {
	// Per-user mode has no user column, so the same line keeps every token in
	// run — peeling one off here would invent a user the crontab never named.
	res := parseCron(t, "@reboot root /usr/bin/warmup\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, `run = "root /usr/bin/warmup"`)
	mustNotContain(t, out, `user =`)
}

func TestCronWrapperNameSkip(t *testing.T) {
	res := parseCron(t, "0 1 * * * /usr/bin/nice -n 19 /opt/sync.sh\n", CronOptions{})
	mustContain(t, res.TOML(), "[tasks.sync]")
}

func TestCronSkipsBlankAndComments(t *testing.T) {
	res := parseCron(t, "\n# a header comment\n\n0 1 * * * /bin/job\n", CronOptions{})
	tasks := res.Tally().Tasks
	if tasks != 1 {
		t.Fatalf("expected 1 task, got %d", tasks)
	}
	// The detached comment (blank line between it and the job) is not a description.
	mustNotContain(t, res.TOML(), "a header comment")
}

func TestCronDetectHeaderLegendSwitchesToSystem(t *testing.T) {
	// The classic /etc/crontab legend turns on system parsing, so the user
	// column is split out rather than folded into the command.
	in := "# m h dom mon dow user  command\n0 5 * * * deploy /usr/bin/cleanup\n"
	res := parseCron(t, in, CronOptions{Detect: true})
	out := res.TOML()
	mustContain(t, out, `user = "deploy"`)
	mustContain(t, out, `run = "/usr/bin/cleanup"`)
	mustNotContain(t, out, `run = "deploy /usr/bin/cleanup"`)
	// The legend line is not kept as a description.
	mustNotContain(t, out, "dom mon dow")
}

func TestCronDetectAmbiguousUserColumnWarns(t *testing.T) {
	// No header legend, but the sixth token looks like a username. We must not
	// silently fold it into run; instead flag it (inline banner + note).
	res := parseCron(t, "0 5 * * * deploy /usr/bin/cleanup\n", CronOptions{Detect: true})
	out := res.TOML()
	mustContain(t, out, "TODO")
	mustContain(t, out, "--system")
	// Per-user parsing is unchanged: the token stays in run, nothing invented.
	mustContain(t, out, `run = "deploy /usr/bin/cleanup"`)
	mustNotContain(t, out, `user =`)
	if !hasBlockingNote(res, "system crontab") {
		t.Fatalf("expected system-crontab note, got %+v", allNotes(res))
	}
}

func TestCronDetectDoesNotWarnOnInterpreterCommand(t *testing.T) {
	// `python /app/x.py` is an ordinary per-user job — the sixth token is an
	// interpreter, not a username, so detection must stay quiet.
	res := parseCron(t, "0 5 * * * python /app/x.py\n", CronOptions{Detect: true})
	out := res.TOML()
	mustNotContain(t, out, "TODO")
	mustNotContain(t, out, "--system")
	if hasBlockingNote(res, "system crontab") {
		t.Fatalf("did not expect a system-crontab note, got %+v", allNotes(res))
	}
}

func TestCronForcedPerUserSilencesDetection(t *testing.T) {
	// System=false with detection off (the --system=false path) must parse
	// per-user and never warn, even on a username-shaped token.
	res := parseCron(t, "0 5 * * * deploy /usr/bin/cleanup\n", CronOptions{System: false, Detect: false})
	out := res.TOML()
	mustContain(t, out, `run = "deploy /usr/bin/cleanup"`)
	mustNotContain(t, out, "TODO")
	if hasBlockingNote(res, "system crontab") {
		t.Fatalf("did not expect a note when detection is off, got %+v", allNotes(res))
	}
}
