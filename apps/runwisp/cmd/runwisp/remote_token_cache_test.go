// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeJWT builds an unsigned-but-well-formed JWT carrying the given exp claim
// (unix seconds). Only the payload segment is meaningful to jwtExpiry.
func fakeJWT(exp int64) string {
	payload, _ := json.Marshal(map[string]int64{"exp": exp})
	seg := base64.RawURLEncoding.EncodeToString(payload)
	return "header." + seg + ".signature"
}

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

	token := fakeJWT(time.Now().Add(time.Hour).Unix())
	storeCachedToken(url, token)

	assert.Equal(t, token, loadCachedToken(url))
	// Trailing-slash variants map to the same entry.
	assert.Equal(t, token, loadCachedToken(url+"/"))
}

func TestTokenCache_DropsExpired(t *testing.T) {
	useTempCacheDir(t)
	const url = "https://runwisp.example.com"

	storeCachedToken(url, fakeJWT(time.Now().Add(-time.Minute).Unix()))
	assert.Empty(t, loadCachedToken(url), "an expired token must not be returned")
}

func TestTokenCache_UnknownExpiryIsKept(t *testing.T) {
	useTempCacheDir(t)
	const url = "https://runwisp.example.com"

	// A token with no parseable exp claim has ExpiresAt 0 ("unknown") and is
	// still handed back — the trigger path's 401-retry is the safety net.
	storeCachedToken(url, "not-a-jwt")
	assert.Equal(t, "not-a-jwt", loadCachedToken(url))
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
	token := fakeJWT(time.Now().Add(time.Hour).Unix())
	storeCachedToken("https://x", token)
	assert.Equal(t, token, loadCachedToken("https://x"))
}

func TestTokenCache_MissingURLInPopulatedCache(t *testing.T) {
	useTempCacheDir(t)
	storeCachedToken("https://a.example.com", fakeJWT(time.Now().Add(time.Hour).Unix()))

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
		storeCachedToken("https://x", fakeJWT(time.Now().Add(time.Hour).Unix()))
	})
	assert.Empty(t, loadCachedToken("https://x"), "nothing was persisted")
}

func TestJWTExpiry(t *testing.T) {
	t.Run("reads the exp claim", func(t *testing.T) {
		assert.Equal(t, int64(1700000000), jwtExpiry(fakeJWT(1700000000)))
	})
	t.Run("wrong segment count is unknown", func(t *testing.T) {
		assert.Equal(t, int64(0), jwtExpiry("only.two"))
	})
	t.Run("non-base64 payload is unknown", func(t *testing.T) {
		assert.Equal(t, int64(0), jwtExpiry("header.!!!not-base64!!!.sig"))
	})
	t.Run("non-JSON payload is unknown", func(t *testing.T) {
		seg := base64.RawURLEncoding.EncodeToString([]byte("not json"))
		assert.Equal(t, int64(0), jwtExpiry("header."+seg+".sig"))
	})
}

func TestTokenCache_SeparateURLs(t *testing.T) {
	useTempCacheDir(t)
	a := fakeJWT(time.Now().Add(time.Hour).Unix())
	b := fakeJWT(time.Now().Add(time.Hour).Unix())
	storeCachedToken("https://a.example.com", a)
	storeCachedToken("https://b.example.com", b)
	assert.Equal(t, a, loadCachedToken("https://a.example.com"))
	assert.Equal(t, b, loadCachedToken("https://b.example.com"))
}
