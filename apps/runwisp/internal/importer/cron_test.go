// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package importer

import (
	"strings"
	"testing"
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
	for _, n := range r.Notes {
		if n.Level == LevelAttention && strings.Contains(n.Message, substr) {
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
	mustContain(t, out, "[defaults]")
	mustContain(t, out, `shell = "/bin/bash"`)
	mustContain(t, out, "[defaults.env]")
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
	mustContain(t, out, "[scheduler]")
	mustContain(t, out, `timezone = "Europe/Bratislava"`)
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
