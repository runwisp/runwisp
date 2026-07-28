// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "testing"

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
