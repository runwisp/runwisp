// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/importer"
)

// This file backs first-run cron detection: scanning the machine for
// crontabs before any runwisp.toml exists, so the interactive scaffold
// (cmd/runwisp/firstrun.go) can offer to read them the same way it already
// offers an adjacent docker-compose file. It is a thin driver over the exact
// machinery `[daemon] include_cron` uses once a config exists —
// resolveCronIncludes and parseCronSource — so first-run detection and live
// parsing can never disagree about what counts as a job.

// DefaultCronPatterns returns the include_cron patterns the interactive
// scaffold would offer for this account.
//
// At euid 0, that is every system source plus every recognized per-user
// spool: a root daemon can become anyone, so nothing is out of reach.
// Anyone else gets only their own spool file. /etc/crontab and cron.d carry
// jobs with their own user column, and most of those columns name someone
// other than the caller — an unprivileged daemon offered those would
// scaffold a config whose every run fails at exec time with "only root can
// become another user" (see assertSpoolRunnable). username == "" (the
// account lookup failed) yields no patterns at all rather than guessing.
func DefaultCronPatterns(euid int, username string) []string {
	if euid == 0 {
		patterns := []string{importer.SystemCrontabPath, importer.SystemCronDirGlob()}
		for _, dir := range importer.UserSpoolDirs() {
			patterns = append(patterns, dir+"/*")
		}
		return patterns
	}
	if username == "" {
		return nil
	}
	patterns := make([]string, 0, len(importer.UserSpoolDirs()))
	for _, dir := range importer.UserSpoolDirs() {
		patterns = append(patterns, dir+"/"+username)
	}
	return patterns
}

// CronScan is what first-run detection found before any daemon or config
// exists.
type CronScan struct {
	// Globs are the patterns DefaultCronPatterns resolved, restated for a
	// caller that wants to log or print them.
	Globs []string
	// Files are the crontabs that were actually read — matched, trusted, and
	// parseable, whether or not every job inside one came out live.
	Files []string
	// Jobs is the total job count across every file in Files. What an
	// operator reads as "N cron jobs on this box."
	Jobs int
	// Live is how many of those jobs would actually run under include_cron
	// today — Jobs minus Live is the count that would need a fix first, the
	// same distinction `runwisp reload`'s CronFinding.Skipped draws once a
	// config exists.
	Live int
	// Blocked lists one reason per crontab RunWisp refused outright — an
	// untrusted file, one it can't become the owner of. These are exactly
	// the per-file refusals mergeCronSources turns into a CronFinding once
	// a config exists; here, with no config to load around them yet, they
	// are the caller's signal to fall back to the plain starter instead of
	// scaffolding an include_cron that can't do anything on its first load.
	Blocked []string
}

// ScanCronSources globs patterns exactly like `[daemon] include_cron` would
// once cfgPath exists, and tallies what it finds.
//
// Never fatal: this runs before any daemon exists, so there is nobody to
// report a hard error to but the interactive prompt, and "found nothing" is
// exactly the no-cron-here case the caller already has to handle. A
// malformed pattern or an unresolvable glob just yields an empty CronScan.
func ScanCronSources(patterns []string, cfgPath string) CronScan {
	var scan CronScan
	if len(patterns) == 0 {
		return scan
	}
	rootDir := filepath.Dir(cfgPath)
	globs, matched, _, err := resolveCronIncludes(patterns, rootDir, cfgPath)
	if err != nil {
		return scan
	}
	scan.Globs = globs

	owned := importer.Owned{}
	for _, path := range matched {
		res, err := parseCronSource(path, owned)
		if err != nil {
			scan.Blocked = append(scan.Blocked, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		scan.Files = append(scan.Files, path)
		claimOwned(owned, res)
		for _, it := range res.Items() {
			scan.Jobs++
			if it.LiveEligible() {
				scan.Live++
			}
		}
	}
	return scan
}
