// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testSessions mints session tokens for the fake daemons in this package. They
// never check the MAC; only the token format and expiry matter to the CLI.
var testSessions, _ = auth.NewService("pw", []byte("test-session-key"), nil)

// freshToken is what the fake daemons hand out on a successful CHAP login.
var freshToken = testSessions.IssueToken(time.Hour)

// useTempCacheDir points os.UserCacheDir at a temp directory across platforms
// (XDG_CACHE_HOME on Linux, HOME on macOS).
func useTempCacheDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("HOME", dir)
}

func TestTokenCache_RoundTrip(t *testing.T) {
	useTempCacheDir(t)
	const url = "https://runwisp.example.com"

	assert.Empty(t, loadCachedToken(url), "no token before any store")

	token := testSessions.IssueToken(time.Hour)
	storeCachedToken(url, token)

	assert.Equal(t, token, loadCachedToken(url))
	// Trailing-slash variants map to the same entry.
	assert.Equal(t, token, loadCachedToken(url+"/"))
}

func TestTokenCache_DropsExpired(t *testing.T) {
	useTempCacheDir(t)
	const url = "https://runwisp.example.com"

	storeCachedToken(url, testSessions.IssueToken(-time.Minute))
	assert.Empty(t, loadCachedToken(url), "an expired token must not be returned")
}

func TestTokenCache_UnreadableExpiryIsMiss(t *testing.T) {
	useTempCacheDir(t)
	const url = "https://runwisp.example.com"

	// A token whose expiry can't be read (e.g. a JWT cached by an older
	// release) is a miss, so the caller handshakes instead of sending it.
	storeCachedToken(url, "header.payload.signature")
	assert.Empty(t, loadCachedToken(url))
}

func TestTokenCache_CorruptFile(t *testing.T) {
	useTempCacheDir(t)
	cacheBase, err := os.UserCacheDir()
	require.NoError(t, err)
	path := filepath.Join(cacheBase, "runwisp", "tokens.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0600))

	// Corrupt cache reads as "no token", and a subsequent store overwrites it.
	assert.Empty(t, loadCachedToken("https://x"))
	token := testSessions.IssueToken(time.Hour)
	storeCachedToken("https://x", token)
	assert.Equal(t, token, loadCachedToken("https://x"))
}

func TestTokenCache_MissingURLInPopulatedCache(t *testing.T) {
	useTempCacheDir(t)
	storeCachedToken("https://a.example.com", testSessions.IssueToken(time.Hour))

	// A different daemon URL has no entry in the otherwise-valid cache.
	assert.Empty(t, loadCachedToken("https://b.example.com"))
}

// TestTokenCache_NoCacheDir forces os.UserCacheDir to fail (no XDG_CACHE_HOME
// and no HOME) so both load and store fall through their error paths without a
// panic — caching is best-effort and a missing cache dir must never break exec.
func TestTokenCache_NoCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "")

	_, err := tokenCachePath()
	require.Error(t, err, "no cache dir is resolvable")

	assert.Empty(t, loadCachedToken("https://x"))
	assert.NotPanics(t, func() { storeCachedToken("https://x", "tok") })
}

// TestTokenCache_UnwritablePath points the cache at a path whose parent is a
// regular file, so EnsureDir / WriteSecretFile fail. The store is swallowed and
// the (unwritten) token reads back as absent.
func TestTokenCache_UnwritablePath(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("not a dir"), 0600))
	// os.UserCacheDir resolves to <blocker>, so tokenCachePath is
	// <blocker>/runwisp/tokens.json — and <blocker> is a file, not a dir.
	t.Setenv("XDG_CACHE_HOME", blocker)
	t.Setenv("HOME", blocker)

	assert.NotPanics(t, func() {
		storeCachedToken("https://x", testSessions.IssueToken(time.Hour))
	})
	assert.Empty(t, loadCachedToken("https://x"), "nothing was persisted")
}

func TestTokenCache_SeparateURLs(t *testing.T) {
	useTempCacheDir(t)
	a := testSessions.IssueToken(time.Hour)
	b := testSessions.IssueToken(time.Hour)
	storeCachedToken("https://a.example.com", a)
	storeCachedToken("https://b.example.com", b)
	assert.Equal(t, a, loadCachedToken("https://a.example.com"))
	assert.Equal(t, b, loadCachedToken("https://b.example.com"))
}
