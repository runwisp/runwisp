// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolatedCacheDir points os.UserCacheDir at a fresh temp dir on every supported
// platform: Linux honours XDG_CACHE_HOME, macOS derives the cache from HOME. We
// set both so the pin file never touches a developer's real cache.
func isolatedCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
}

func TestCertPinStore_LoadMissingFileIsFirstContact(t *testing.T) {
	isolatedCacheDir(t)
	_, ok := certPinStore{}.Load("https://daemon.example:9477")
	assert.False(t, ok)
}

func TestCertPinStore_StoreThenLoad(t *testing.T) {
	isolatedCacheDir(t)
	const key = "https://daemon.example:9477"
	certPinStore{}.Store(key, "abc123")

	got, ok := certPinStore{}.Load(key)
	require.True(t, ok)
	assert.Equal(t, "abc123", got)
}

func TestCertPinStore_StorePreservesOtherPins(t *testing.T) {
	isolatedCacheDir(t)
	certPinStore{}.Store("https://a.example", "fp-a")
	certPinStore{}.Store("https://b.example", "fp-b")

	gotA, okA := certPinStore{}.Load("https://a.example")
	require.True(t, okA)
	assert.Equal(t, "fp-a", gotA)
	gotB, okB := certPinStore{}.Load("https://b.example")
	require.True(t, okB)
	assert.Equal(t, "fp-b", gotB)
}

func TestCertPinStore_LoadCorruptFileIsFirstContact(t *testing.T) {
	isolatedCacheDir(t)
	path, err := pinStorePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o600))

	_, ok := certPinStore{}.Load("https://daemon.example")
	assert.False(t, ok)
}

func TestCertPinStore_StoreOverwritesCorruptFile(t *testing.T) {
	isolatedCacheDir(t)
	path, err := pinStorePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("garbage"), 0o600))

	certPinStore{}.Store("https://daemon.example", "fp")
	got, ok := certPinStore{}.Load("https://daemon.example")
	require.True(t, ok)
	assert.Equal(t, "fp", got)
}

func TestCertPinStore_LoadEmptyFingerprintIsFirstContact(t *testing.T) {
	isolatedCacheDir(t)
	path, err := pinStorePath()
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(`{"https://daemon.example":""}`), 0o600))

	_, ok := certPinStore{}.Load("https://daemon.example")
	assert.False(t, ok)
}
