// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package importer

import "path/filepath"

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

// CronOwnsPath reports whether a live system cron daemon would read this file
// itself. It answers one question for the scheduler: is this crontab something
// cron is already firing, or a file only RunWisp knows about?
//
// `include_cron` can legitimately point at a crontab-format file that lives
// nowhere cron looks — /opt/myapp/jobs.cron is a normal thing to keep under an
// app's own directory — and those jobs are RunWisp's alone even while cron runs.
// Holding them would stop them dead for no reason, so the answer has to be
// per-file rather than "is cron running at all".
//
// Deliberately broader than UserSpoolOwner for a spool file: any file sitting
// directly in a spool directory counts, whatever it is named. The two ways to be
// wrong here are not symmetric — treating a cron-owned file as ours means both
// schedulers fire the job, which is invisible, while treating one of ours as
// cron's means it is held and badged as held, which the operator can see and act
// on. When in doubt, say cron owns it.
func CronOwnsPath(path string) bool {
	if path == "" || path == "-" {
		return false
	}
	clean := filepath.Clean(path)
	if IsSystemCrontabPath(clean) {
		return true
	}
	return IsSpoolCrontabDir(filepath.Dir(clean))
}

// CronUnits returns every systemd unit name cron ships under across the distros
// RunWisp targets: Debian/Ubuntu's cron.service, and cronie's crond.service
// (RHEL/Fedora) or cronie.service (some derivatives).
//
// One list, because two callers ask the same question for opposite reasons and
// disagreeing is a silent double-fire: internal/config probes these to decide
// whether cron is live and its jobs must be held, and internal/autostart probes
// them to decide which unit `--take-over-cron` masks. A unit missing from the
// hold-side list but present on the mask-side one is a box where cron runs, the
// jobs fire twice, and nothing says so.
func CronUnits() []string {
	return []string{"cron.service", "crond.service", "cronie.service"}
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
