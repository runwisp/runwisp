// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultCronPatterns_Root pins the euid-0 branch: every system source
// plus every recognized spool, since a root daemon can become anyone.
func TestDefaultCronPatterns_Root(t *testing.T) {
	got := DefaultCronPatterns(0, "root")
	assert.Contains(t, got, importer.SystemCrontabPath)
	assert.Contains(t, got, importer.SystemCronDirGlob())
	for _, dir := range importer.UserSpoolDirs() {
		assert.Contains(t, got, dir+"/*")
	}
}

// TestDefaultCronPatterns_Unprivileged pins the non-root branch: only this
// account's own spool file across every recognized layout, never
// /etc/crontab or cron.d — those carry jobs for other users that an
// unprivileged daemon can never become.
func TestDefaultCronPatterns_Unprivileged(t *testing.T) {
	got := DefaultCronPatterns(1000, "alice")
	assert.NotContains(t, got, importer.SystemCrontabPath)
	assert.NotContains(t, got, importer.SystemCronDirGlob())
	for _, dir := range importer.UserSpoolDirs() {
		assert.Contains(t, got, dir+"/alice")
	}
}

// TestDefaultCronPatterns_NoUsername: an unresolvable account lookup must
// yield no patterns at all, not a guess.
func TestDefaultCronPatterns_NoUsername(t *testing.T) {
	assert.Empty(t, DefaultCronPatterns(1000, ""))
}

func TestScanCronSources_NoPatterns(t *testing.T) {
	scan := ScanCronSources(nil, filepath.Join(t.TempDir(), "runwisp.toml"))
	assert.Zero(t, scan.Jobs)
	assert.Empty(t, scan.Files)
}

func TestScanCronSources_CountsJobsAndLiveEligibility(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crontab"),
		[]byte("0 3 * * * /usr/local/bin/good.sh\n"), 0o644))

	scan := ScanCronSources([]string{"crontab"}, filepath.Join(dir, "runwisp.toml"))
	assert.Equal(t, []string{filepath.Join(dir, "crontab")}, scan.Files)
	assert.Equal(t, 1, scan.Jobs)
	assert.Equal(t, 1, scan.Live)
	assert.Empty(t, scan.Blocked)
}

// TestScanCronSources_UnreadableFileIsBlockedNotFatal: unlike
// mergeCronSources, which is fail-open across a whole config load,
// ScanCronSources runs before any config exists — a refused file just lands
// in Blocked so the first-run prompt can explain why it isn't offering to
// scaffold include_cron.
func TestScanCronSources_UnreadableFileIsBlockedNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a 0000 file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "crontab")
	require.NoError(t, os.WriteFile(bad, []byte("0 3 * * * /bin/true\n"), 0o000))

	scan := ScanCronSources([]string{"crontab"}, filepath.Join(dir, "runwisp.toml"))
	assert.Zero(t, scan.Jobs)
	assert.Empty(t, scan.Files)
	require.Len(t, scan.Blocked, 1)
	assert.Contains(t, scan.Blocked[0], bad)
}
