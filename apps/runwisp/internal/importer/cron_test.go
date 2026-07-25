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

func hasAttentionNote(r *Result, substr string) bool {
	return hasNote(r, LevelAttention, substr)
}

func hasNote(r *Result, level Level, substr string) bool {
	for _, n := range r.Notes {
		if n.Level == level && strings.Contains(n.Message, substr) {
			return true
		}
	}
	return false
}

func TestCronBasicFiveField(t *testing.T) {
	res := parseCron(t, "30 2 * * * /usr/local/bin/backup.sh --full\n", CronOptions{})
	out := res.TOML()
	mustContain(t, out, "[tasks.backup]")
	mustContain(t, out, `cron = "30 2 * * *"`)
	mustContain(t, out, `run = "/usr/local/bin/backup.sh --full"`)

	tasks, services := res.Counts()
	if tasks != 1 || services != 0 {
		t.Fatalf("counts: got tasks=%d services=%d, want 1/0", tasks, services)
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
	if !hasAttentionNote(res, "MAILTO") {
		t.Fatalf("expected MAILTO attention note, got %+v", res.Notes)
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
	if !hasAttentionNote(res, "absolute path") {
		t.Fatalf("expected relative-shell attention note, got %+v", res.Notes)
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
	if tasks, _ := res.Counts(); tasks != 0 {
		t.Fatalf("expected the matching job to be skipped, got %d tasks", tasks)
	}
	mustNotContain(t, res.TOML(), "[tasks.backup]")
	if !hasNote(res, LevelInfo, "skipped re-importing") {
		t.Fatalf("expected a skip note, got %+v", res.Notes)
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
	if !hasNote(res, LevelInfo, "imported this one as") {
		t.Fatalf("expected a rename note, got %+v", res.Notes)
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
	if !hasAttentionNote(res, "didn't parse") {
		t.Fatalf("expected parse-failure note, got %+v", res.Notes)
	}
	if !res.Items()[0].Attention {
		t.Fatalf("expected item flagged for attention")
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
	tasks, _ := res.Counts()
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
	if !hasAttentionNote(res, "system crontab") {
		t.Fatalf("expected system-crontab attention note, got %+v", res.Notes)
	}
}

func TestCronDetectDoesNotWarnOnInterpreterCommand(t *testing.T) {
	// `python /app/x.py` is an ordinary per-user job — the sixth token is an
	// interpreter, not a username, so detection must stay quiet.
	res := parseCron(t, "0 5 * * * python /app/x.py\n", CronOptions{Detect: true})
	out := res.TOML()
	mustNotContain(t, out, "TODO")
	mustNotContain(t, out, "--system")
	if hasAttentionNote(res, "system crontab") {
		t.Fatalf("did not expect a system-crontab note, got %+v", res.Notes)
	}
}

func TestCronForcedPerUserSilencesDetection(t *testing.T) {
	// System=false with detection off (the --system=false path) must parse
	// per-user and never warn, even on a username-shaped token.
	res := parseCron(t, "0 5 * * * deploy /usr/bin/cleanup\n", CronOptions{System: false, Detect: false})
	out := res.TOML()
	mustContain(t, out, `run = "deploy /usr/bin/cleanup"`)
	mustNotContain(t, out, "TODO")
	if hasAttentionNote(res, "system crontab") {
		t.Fatalf("did not expect a note when detection is off, got %+v", res.Notes)
	}
}
