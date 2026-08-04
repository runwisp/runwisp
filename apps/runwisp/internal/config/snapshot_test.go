// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSnapshotConfig(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestSnapshot_FreshConfigIsNotStale(t *testing.T) {
	dir := t.TempDir()
	path := writeSnapshotConfig(t, dir, "[tasks.t]\nrun = \"echo hi\"\n")
	cfg, err := Load(path)
	require.NoError(t, err)

	snap := NewSnapshot(path, cfg, time.Now())
	assert.False(t, snap.Stale())
	assert.False(t, snap.LoadedAt().IsZero())
}

func TestSnapshot_EditedConfigIsStale(t *testing.T) {
	dir := t.TempDir()
	path := writeSnapshotConfig(t, dir, "[tasks.t]\nrun = \"echo hi\"\n")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())

	require.NoError(t, os.WriteFile(path, []byte("[tasks.t]\nrun = \"echo bye\"\n"), 0o600))
	assert.True(t, snap.Stale())
}

func TestSnapshot_DeletedConfigIsStale(t *testing.T) {
	dir := t.TempDir()
	path := writeSnapshotConfig(t, dir, "[tasks.t]\nrun = \"echo hi\"\n")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())

	require.NoError(t, os.Remove(path))
	assert.True(t, snap.Stale())
}

func TestSnapshot_EditedEnvFileIsStale(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "secrets.env")
	require.NoError(t, os.WriteFile(envPath, []byte("TOKEN=one\n"), 0o600))
	// env_file is referenced relative to the config dir, mirroring loadEnvFile.
	path := writeSnapshotConfig(t, dir, "[tasks.t]\nrun = \"echo hi\"\nenv_file = \"secrets.env\"\n")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.WriteFile(envPath, []byte("TOKEN=two\n"), 0o600))
	assert.True(t, snap.Stale())
}

func TestSnapshot_EditedIncludedFileIsStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":  "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/a.toml": "[tasks.a]\nrun = \"echo hi\"\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "conf.d", "a.toml"),
		[]byte("[tasks.a]\nrun = \"echo bye\"\n"), 0o600))
	assert.True(t, snap.Stale())
}

func TestSnapshot_AddedMatchingFileIsStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":  "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/a.toml": "[tasks.a]\nrun = \"echo hi\"\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())
	require.False(t, snap.Stale())

	// A brand-new file matching the glob must flip stale via the re-glob path.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "conf.d", "b.toml"),
		[]byte("[tasks.b]\nrun = \"echo new\"\n"), 0o600))
	assert.True(t, snap.Stale())
}

func TestSnapshot_DeletedIncludedFileIsStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":  "[daemon]\ninclude = [\"conf.d/*.toml\"]\n",
		"conf.d/a.toml": "[tasks.a]\nrun = \"echo hi\"\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.Remove(filepath.Join(dir, "conf.d", "a.toml")))
	assert.True(t, snap.Stale())
}

// TestSnapshot_CrondIneligibleFileInGlobDoesNotFlagStale is the regression test
// for a `status` that said "config changed" forever after a completely untouched
// take-over. An include_cron glob only reads what crond itself would read, so
// /etc/cron.d/.placeholder — a file the Debian cron package installs on every box
// — is not in the boot set; Stale re-globbed with a plain filepath.Glob, found it
// every time, and could never match.
func TestSnapshot_CrondIneligibleFileInGlobDoesNotFlagStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":          "[daemon]\ninclude_cron = [\"crontabs/*\"]\n",
		"crontabs/backup":       "17 3 * * * /usr/bin/backup\n",
		"crontabs/.placeholder": "",
		"crontabs/old.dpkg-old": "17 3 * * * /usr/bin/stale\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "crontabs", "backup")}, cfg.CronFiles(),
		"only the crond-eligible crontab should have been read")

	snap := NewSnapshot(path, cfg, time.Now())
	assert.False(t, snap.Stale(), "a file crond ignores must not read as a config change")
}

// TestSnapshot_RefusedCronSourceInGlobDoesNotFlagStale is the same false positive
// from the other direction: a crontab the glob matched and crond would run, which
// RunWisp then refused to take commands from. It is reported as a finding, not as
// a config that keeps changing on its own.
func TestSnapshot_RefusedCronSourceInGlobDoesNotFlagStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":    "[daemon]\ninclude_cron = [\"crontabs/*\"]\n",
		"crontabs/backup": "17 3 * * * /usr/bin/backup\n",
		"crontabs/web":    "17 4 * * * /usr/bin/web\n",
	})
	// World-writable: anyone on the box could put commands in it, so the loader
	// refuses the source (see assertCronFileTrusted) and keeps the rest.
	require.NoError(t, os.Chmod(filepath.Join(dir, "crontabs", "web"), 0o666))

	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "crontabs", "backup")}, cfg.CronFiles())
	require.NotEmpty(t, cfg.CronFindings, "the refusal must be reported")

	snap := NewSnapshot(path, cfg, time.Now())
	assert.False(t, snap.Stale())
}

// TestSnapshot_AddedCrontabIsStale is the half the eligibility filter must not
// swallow: a real crontab dropped into a watched directory is still a change the
// operator has not reloaded.
func TestSnapshot_AddedCrontabIsStale(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":    "[daemon]\ninclude_cron = [\"crontabs/*\"]\n",
		"crontabs/backup": "17 3 * * * /usr/bin/backup\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	snap := NewSnapshot(path, cfg, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.WriteFile(filepath.Join(dir, "crontabs", "reports"),
		[]byte("0 6 * * * /usr/bin/reports\n"), 0o600))
	assert.True(t, snap.Stale())
}

// TestSnapshot_DotfileIncludeGlobIsNotCrondFiltered guards the split: crond's
// naming rule belongs to include_cron alone, so a plain [daemon].include glob
// that matches a dotfile keeps behaving exactly as it did.
func TestSnapshot_DotfileIncludeGlobIsNotCrondFiltered(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":        "[daemon]\ninclude = [\"conf.d/*\"]\n",
		"conf.d/.hidden.toml": "[tasks.hidden]\nrun = \"echo hi\"\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "conf.d", ".hidden.toml")}, cfg.includeFiles,
		"a plain include reads whatever the glob matches")

	snap := NewSnapshot(path, cfg, time.Now())
	assert.False(t, snap.Stale())
}

func TestSnapshot_MissingFileAppearingIsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	// Snapshot a path that does not exist (cloud mode boots without a
	// runwisp.toml); the file showing up later must read as a change.
	snap := NewSnapshot(path, nil, time.Now())
	require.False(t, snap.Stale())

	require.NoError(t, os.WriteFile(path, []byte("[tasks.t]\nrun = \"echo hi\"\n"), 0o600))
	assert.True(t, snap.Stale())
}
