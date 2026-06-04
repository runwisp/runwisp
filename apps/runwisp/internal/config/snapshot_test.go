// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
