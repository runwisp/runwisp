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
