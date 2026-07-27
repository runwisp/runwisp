// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "testing"

// TestIsSystemCrontabPath pins the one heuristic that decides whether a file's
// sixth field is a username or part of the command — get it wrong and either a
// command loses its first word or a username is executed.
func TestIsSystemCrontabPath(t *testing.T) {
	cases := map[string]bool{
		"/etc/crontab":              true,
		"/etc/cron.d/backup":        true,
		"/etc/cron.d/sub/thing":     true,
		"/home/me/crontab":          false,
		"crontab":                   false,
		"-":                         false,
		"":                          false,
		"/etc/cron.daily/logrotate": false, // scripts, not crontab-format
	}
	for path, want := range cases {
		if got := IsSystemCrontabPath(path); got != want {
			t.Errorf("IsSystemCrontabPath(%q) = %v, want %v", path, got, want)
		}
	}
}
