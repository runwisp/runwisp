// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import (
	"strings"
	"testing"
)

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

// TestUserSpoolOwner pins the other path heuristic: who a crontab's jobs run as
// when the file carries no user column and the answer is its own filename.
//
// The failure it exists to prevent is asymmetric. A false negative loses a run-as
// user and the jobs run as the daemon; a false positive invents an identity out of
// a filename. The ownership check downstream catches the second, but only for paths
// this function claims — so the claim has to be narrow.
func TestUserSpoolOwner(t *testing.T) {
	cases := map[string]string{
		// The two conventional layouts.
		"/var/spool/cron/crontabs/alice": "alice", // Debian, Ubuntu
		"/var/spool/cron/bob":            "bob",   // RHEL, SUSE
		"/var/spool/cron/crontabs/svc-1": "svc-1",
		"/var/spool/cron/crontabs/a_b.c": "a_b.c",

		// Not a spool: no owner to derive, and the jobs run as the daemon.
		"/etc/crontab":                        "",
		"/etc/cron.d/backup":                  "",
		"/var/spool/cron/crontabs/sub/deeper": "",
		"/home/alice/crontab":                 "",
		"/var/spool/anacron/cron.daily":       "",
		"-":                                   "",
		"":                                    "",

		// In a spool directory, but not a name an account can have. Guessing an
		// identity from these would be inventing one.
		"/var/spool/cron/crontabs/alice bob":   "",
		"/var/spool/cron/crontabs/tmp:lock":    "",
		"/var/spool/cron/crontabs/back\\slash": "",
	}
	for path, want := range cases {
		got, ok := UserSpoolOwner(path)
		if got != want || ok != (want != "") {
			t.Errorf("UserSpoolOwner(%q) = (%q, %v), want (%q, %v)", path, got, ok, want, want != "")
		}
	}
}

// TestIsSpoolCrontabDir pins the directory half of the spool check, split out
// so a caller with only a directory (no filename yet) can still ask the
// question — `[daemon] include_cron`'s glob-eligibility filter needs exactly
// that to avoid guessing "spool" for a directory that merely happens to be
// named crontabs.
func TestIsSpoolCrontabDir(t *testing.T) {
	cases := map[string]bool{
		"/var/spool/cron/crontabs": true,
		"/var/spool/cron":          true,
		"/var/spool/cron/":         true,
		"/var/spool/anacron":       false,
		"/home/alice/crontabs":     false,
		"":                         false,
	}
	for dir, want := range cases {
		if got := IsSpoolCrontabDir(dir); got != want {
			t.Errorf("IsSpoolCrontabDir(%q) = %v, want %v", dir, got, want)
		}
	}
}

// TestSpoolCrontabRunsAsItsOwner: a spool crontab has no user column, so without
// CronOptions.User every job would run as the daemon — the same commands under the
// wrong identity, writing to the wrong home, with nothing saying so.
func TestSpoolCrontabRunsAsItsOwner(t *testing.T) {
	const in = "0 3 * * * /usr/local/bin/backup.sh\n@reboot /usr/local/bin/warmup\n"
	res, err := ParseCrontab(strings.NewReader(in), CronOptions{User: "alice"})
	if err != nil {
		t.Fatalf("ParseCrontab: %v", err)
	}
	toml := res.TOML()
	if want := `user = "alice"`; strings.Count(toml, want) != 2 {
		t.Errorf("expected both jobs to carry %s, got:\n%s", want, toml)
	}
}

// TestSpoolOwnerNeverOverridesAUserColumn: System and User are mutually exclusive
// by construction, but if a caller ever set both, the line's own column is the more
// specific statement and has to win.
func TestSpoolOwnerNeverOverridesAUserColumn(t *testing.T) {
	const in = "0 3 * * * deploy /usr/local/bin/backup.sh\n"
	res, err := ParseCrontab(strings.NewReader(in), CronOptions{System: true, User: "alice"})
	if err != nil {
		t.Fatalf("ParseCrontab: %v", err)
	}
	toml := res.TOML()
	if !strings.Contains(toml, `user = "deploy"`) || strings.Contains(toml, `user = "alice"`) {
		t.Errorf("the line's user column must win over the file's owner, got:\n%s", toml)
	}
}
