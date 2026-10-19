// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// stockUbuntuCron is what a stock Ubuntu desktop's /etc/crontab and /etc/cron.d
// schedule: on a systemd box with anacron, only the hourly line does anything.
const stockUbuntuCron = "17 *	* * *	root	cd / && run-parts --report /etc/cron.hourly\n" +
	"25 6	* * *	root	test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }\n" +
	"47 6	* * 7	root	test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.weekly; }\n" +
	"30 7-23 * * *   root	if [ -x /etc/init.d/anacron ] && ! [ -d /run/systemd/system ]; then exec /usr/sbin/invoke-rc.d anacron start >/dev/null; fi\n" +
	"30 3 * * 0 root test -e /run/systemd/system || SERVICE_MODE=1 /usr/libexec/e2fsprogs/e2scrub_all_cron\n"

// hostStat answers stat calls from an in-memory file tree rooted at /.
func hostStat(root fstest.MapFS) func(string) (fs.FileInfo, error) {
	return func(p string) (fs.FileInfo, error) { return fs.Stat(root, strings.TrimPrefix(p, "/")) }
}

// systemdAnacronHost is a systemd-booted box with anacron installed.
var systemdAnacronHost = fstest.MapFS{
	"usr/sbin/anacron":          {Mode: 0o755},
	"etc/init.d/anacron":        {Mode: 0o755},
	"run/systemd/system/x.unit": {},
}

// TestCronGuardedNoopJobsAreLeftOut is the desktop takeover that imported six
// jobs whose own guards make them do nothing on a systemd box with anacron,
// named `run-parts-crontab-2`, `if`, and so on. They are left out with a note
// and no task; the one real job is still imported.
func TestCronGuardedNoopJobsAreLeftOut(t *testing.T) {
	res := parseCron(t, stockUbuntuCron, CronOptions{System: true, HostStat: hostStat(systemdAnacronHost)})
	out := res.TOML()

	mustContain(t, out, "[tasks.cron-hourly]")
	for _, gone := range []string{"cron-daily", "cron-weekly", "anacron", "e2scrub_all_cron"} {
		mustNotContain(t, out, "[tasks."+gone+"]")
	}

	var left []string
	for _, it := range res.Items() {
		if why := it.NoopHere(); why != "" {
			left = append(left, why)
			if it.Status() != StatusSkipped || it.LiveEligible() {
				t.Errorf("a left-out job must be skipped, not live: %+v", it)
			}
		}
	}
	want := []string{
		"/usr/sbin/anacron is executable",
		"/usr/sbin/anacron is executable",
		"/run/systemd/system is a directory",
		"/run/systemd/system exists",
	}
	if len(left) != len(want) {
		t.Fatalf("left out %d jobs, want %d: %q", len(left), len(want), left)
	}
	for i, w := range want {
		if !strings.HasPrefix(left[i], w) {
			t.Errorf("left-out reason %d = %q, want it to start with %q", i, left[i], w)
		}
	}
	if blocked := res.Tally().Blocked; blocked != 0 {
		t.Errorf("leaving a no-op job out needs nobody's attention, got %d blocked", blocked)
	}
}

// TestCronGuardStillImportsWhenTheGuardFallsThrough: the same lines on a box
// without anacron or systemd do real work, so they are imported.
func TestCronGuardStillImportsWhenTheGuardFallsThrough(t *testing.T) {
	res := parseCron(t, stockUbuntuCron, CronOptions{System: true, HostStat: hostStat(fstest.MapFS{})})
	out := res.TOML()
	for _, name := range []string{"cron-hourly", "cron-daily", "cron-weekly", "e2scrub_all_cron"} {
		mustContain(t, out, "[tasks."+name+"]")
	}
	// `[ -x /etc/init.d/anacron ]` is false here, so that one still does nothing.
	mustNotContain(t, out, "[tasks.anacron]")
}

// TestCronGuardIgnoredWithoutHostStat: a crontab that isn't this machine's is
// imported as written, guards and all.
func TestCronGuardIgnoredWithoutHostStat(t *testing.T) {
	res := parseCron(t, stockUbuntuCron, CronOptions{System: true})
	if got := res.Tally().Tasks; got != 5 {
		t.Fatalf("tasks = %d, want all 5 imported: %+v", got, allNotes(res))
	}
}

func TestNoopGuardShapes(t *testing.T) {
	stat := hostStat(systemdAnacronHost)
	cases := []struct {
		command string
		noop    bool
	}{
		{"[ -x /usr/sbin/anacron ] || run-parts /etc/cron.daily", true},
		{"test -f /usr/sbin/anacron || run-parts /etc/cron.daily", true},
		{"test -x /usr/sbin/missing || run-parts /etc/cron.daily", false},
		// `&&` after the guard runs the job when the test holds: not a no-op shape.
		{"test -x /usr/sbin/anacron && run-parts /etc/cron.daily", false},
		{"if [ -d /run/systemd/system ]; then echo hi; fi", false},
		{"if ! [ -d /run/systemd/system ]; then echo hi; fi", true},
		{"if test -x /usr/sbin/missing; then echo hi; fi", true},
		// Relative paths depend on the working directory, not this host.
		{"test -e run/systemd/system || echo hi", false},
		{"test -n /usr/sbin/anacron || echo hi", false},
		{"/usr/bin/backup --all", false},
	}
	for _, c := range cases {
		if _, noop := noopGuard(c.command, stat); noop != c.noop {
			t.Errorf("noopGuard(%q) = %v, want %v", c.command, noop, c.noop)
		}
	}
}
