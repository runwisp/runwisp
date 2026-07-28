// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

// This file is the one place that may know where a crontab lives on disk.
// IsSystemCrontabPath and UserSpoolOwner already read it; first-run cron
// detection (internal/config/crondetect.go) needs the same paths to offer
// include_cron patterns before any config exists, and a second copy of these
// literals would be a second answer to "where does cron keep its files" that
// could silently drift from this one.

// SystemCrontabPath is the one well-known system crontab file. Unlike a
// cron.d *directory*, which has no fixed location and is recognized by name
// wherever it appears (see IsSystemCrontabPath), there is exactly one of
// these per machine.
const SystemCrontabPath = "/etc/crontab"

// SystemCronDirGlob is the canonical system crontab directory, as a glob
// pattern ready to hand to `[daemon] include_cron`.
func SystemCronDirGlob() string {
	return "/etc/cron.d/*"
}

// UserSpoolDirs returns every per-user cron spool directory this codebase
// recognizes, across every OS and distro layout RunWisp supports. Order
// carries no meaning: a crontab's directory alone decides how its owner is
// derived (see UserSpoolOwner), and a real machine has at most one of these
// populated.
//
// An unrecognized layout is not a neutral miss: UserSpoolOwner returns
// ("", false) for it, so cronOptionsFor never sets CronOptions.User, and
// every job in the file then runs as the daemon instead of the account whose
// crontab it is. openSUSE's own spool path was missing here until this list
// existed, silently escalating every one of a user's jobs to daemon
// privilege.
func UserSpoolDirs() []string {
	return []string{
		"/var/spool/cron/crontabs", // Debian, Ubuntu
		"/var/spool/cron",          // RHEL, classic SUSE
		"/var/spool/cron/tabs",     // openSUSE
		"/usr/lib/cron/tabs",       // macOS
		"/var/at/tabs",             // macOS/BSD, alongside at(1)'s spool
	}
}
