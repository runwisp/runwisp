// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"slices"
	"testing"
)

// TestSystemCronDirGlob pins the one literal first-run cron detection needs
// to offer the same /etc/cron.d default that IsSystemCrontabPath recognizes.
func TestSystemCronDirGlob(t *testing.T) {
	if got := SystemCronDirGlob(); got != "/etc/cron.d/*" {
		t.Errorf("SystemCronDirGlob() = %q, want /etc/cron.d/*", got)
	}
	if !IsSystemCrontabPath("/etc/cron.d/backup") {
		t.Error("a file under SystemCronDirGlob's directory must satisfy IsSystemCrontabPath")
	}
}

// TestCronOwnsPath covers the predicate the scheduler's hold gate turns on: a
// job cron reads itself must not also be fired by RunWisp, and a crontab-format
// file cron never looks at must not be held for a cron daemon that isn't
// touching it.
func TestCronOwnsPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"the system crontab", SystemCrontabPath, true},
		{"a file in /etc/cron.d", "/etc/cron.d/backup", true},
		{"a cron.d anywhere in the path", "/usr/local/etc/cron.d/jobs", true},
		{"an uncleaned path to /etc/cron.d", "/etc/cron.d/../cron.d/backup", true},
		{"a debian spool crontab", "/var/spool/cron/crontabs/deploy", true},
		{"an rhel spool crontab", "/var/spool/cron/root", true},
		{"an opensuse spool crontab", "/var/spool/cron/tabs/deploy", true},
		{"a macos spool crontab", "/usr/lib/cron/tabs/riki", true},
		// A spool file is cron's whatever it is named: cron resolves the
		// basename with getpwnam, and guessing that it wouldn't would trade a
		// visible hold for an invisible double fire.
		{"an implausibly named spool file", "/var/spool/cron/crontabs/not!a!user", true},
		{"an app's own crontab", "/opt/myapp/jobs.cron", false},
		{"a crontab in the operator's home", "/home/riki/my.crontab", false},
		// Only files sitting *directly* in a spool count — cron does not
		// recurse, so a subdirectory is nobody's crontab.
		{"a file below a spool directory", "/var/spool/cron/crontabs/sub/deploy", false},
		{"a directory named like a spool elsewhere", "/srv/backup/crontabs/deploy", false},
		{"empty", "", false},
		{"stdin", "-", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CronOwnsPath(tc.path); got != tc.want {
				t.Errorf("CronOwnsPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// TestCronOwnsPathCoversEverySpoolLayout keeps CronOwnsPath from drifting away
// from the spool table the way IsSpoolCrontabDir is already pinned to it: a
// layout added to UserSpoolDirs that the gate does not recognize would let every
// crontab on that distro fire from both schedulers.
func TestCronOwnsPathCoversEverySpoolLayout(t *testing.T) {
	for _, dir := range UserSpoolDirs() {
		if !CronOwnsPath(dir + "/deploy") {
			t.Errorf("CronOwnsPath(%q/deploy) = false, want true (from UserSpoolDirs)", dir)
		}
	}
}

// TestUserSpoolDirsAgreeWithIsSpoolCrontabDir is the guard against the two
// tables drifting apart: every directory this function lists has to be one
// IsSpoolCrontabDir (built from the same list) actually recognizes, or a spool
// layout could be offered by first-run detection and then silently refused —
// or worse, recognized by one and not the other — when a real crontab shows up.
func TestUserSpoolDirsAgreeWithIsSpoolCrontabDir(t *testing.T) {
	dirs := UserSpoolDirs()
	if len(dirs) == 0 {
		t.Fatal("UserSpoolDirs() must not be empty")
	}
	for _, d := range dirs {
		if !IsSpoolCrontabDir(d) {
			t.Errorf("IsSpoolCrontabDir(%q) = false, want true (from UserSpoolDirs)", d)
		}
	}
}

// TestCronUnitsCoversEveryTakeOverTarget is the regression for a real
// double-fire window: internal/config kept its own two-entry list
// (cron.service, crond.service) while internal/autostart probed a third,
// cronie.service. On a box whose cron registers under that name, cron was live
// and reading the crontabs, RunWisp derived no hold, and every job in them
// fired from both schedulers with nothing on any surface saying so — the exact
// invisible failure the hold gate exists to prevent. Both sides read this list
// now, so the only way back to that bug is deleting a name from here.
func TestCronUnitsCoversEveryTakeOverTarget(t *testing.T) {
	units := CronUnits()
	for _, want := range []string{"cron.service", "crond.service", "cronie.service"} {
		if !slices.Contains(units, want) {
			t.Errorf("CronUnits() = %v, missing %q — a live %s would hold nothing", units, want, want)
		}
	}
}

// TestCronUnitsReturnsACopy guards the shared list against a caller that sorts
// or truncates it in place, which would silently shrink what the other caller
// probes.
func TestCronUnitsReturnsACopy(t *testing.T) {
	first := CronUnits()
	first[0] = "clobbered"
	if CronUnits()[0] == "clobbered" {
		t.Error("CronUnits() handed out shared backing state")
	}
}
