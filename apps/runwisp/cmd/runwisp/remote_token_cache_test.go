// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

func TestTokenCache_SeparateURLs(t *testing.T) {
	useTempCacheDir(t)
	a := fakeJWT(time.Now().Add(time.Hour).Unix())
	b := fakeJWT(time.Now().Add(time.Hour).Unix())
	storeCachedToken("https://a.example.com", a)
	storeCachedToken("https://b.example.com", b)
	assert.Equal(t, a, loadCachedToken("https://a.example.com"))
	assert.Equal(t, b, loadCachedToken("https://b.example.com"))
}
