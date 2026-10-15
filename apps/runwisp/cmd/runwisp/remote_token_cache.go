// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/chap"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"
)

// The CLI caches the session token minted by a remote daemon so repeated `runwisp run
// --url` invocations reuse one session instead of re-running CHAP each time.
// Re-handshaking would burn two hits (challenge + login) against the daemon's
// per-IP auth rate limit on every call; reusing the 24h token keeps a script
// that triggers often well under that ceiling. The cache is a best-effort
// optimization: a stale or unreadable entry simply falls through to a fresh
// handshake, and the trigger path re-authenticates on a 401 regardless.

// cacheSkew trims a margin off a token's expiry so we re-handshake slightly
// early rather than send a token the daemon is about to reject.
const cacheSkew = 60 * time.Second

// tokenCachePath returns the per-user cache file path. It lives under the OS
// cache dir, not the daemon's --data dir: a remote client has no data dir of
// its own, and the file is keyed by daemon URL so one CLI can hold sessions
// for several daemons.
func tokenCachePath() (string, error) {
	return runwispCacheFile("tokens.json")
}

// loadCachedToken returns the cached admin session token for baseURL unless it
// is known to be expired, or "" when none is usable. Any error (missing file,
// corrupt JSON, no cache dir) yields "" so the caller falls through to a fresh
// handshake. A token whose expiry can't be read (an older daemon's JWT) is
// returned as-is: the caller re-authenticates if the daemon answers 401, and
// treating it as a miss would redo CHAP on every call against that daemon.
func loadCachedToken(baseURL string) string {
	return loadCacheEntry(apiclient.NormalizeBaseURL(baseURL))
}

// loadCachedViewerToken is loadCachedToken for a read-only session opened with
// password. Viewer tokens live under a key derived from the password that
// minted them, so they are only ever reused by a login with that same password:
// a later admin login to the same URL never inherits a read-only session.
func loadCachedViewerToken(baseURL, password string) string {
	return loadCacheEntry(viewerCacheKey(baseURL, password))
}

func loadCacheEntry(key string) string {
	path, err := tokenCachePath()
	if err != nil {
		return ""
	}
	// The token carries its own expiry, so the cache maps key to token alone.
	token := loadJSONCacheMap[string](path)[key]
	if expiry, ok := auth.TokenExpiry(token); ok && !time.Now().Add(cacheSkew).Before(expiry) {
		return ""
	}
	return token
}

// storeCachedToken persists token, the session password logged in to baseURL
// with, read-modify-writing the cache map. Failures are logged at debug and
// swallowed — caching is an optimization, never a precondition for the trigger.
func storeCachedToken(baseURL, password, token string) {
	path, err := tokenCachePath()
	if err != nil {
		return
	}
	key := apiclient.NormalizeBaseURL(baseURL)
	if auth.IsViewerToken(token) {
		key = viewerCacheKey(baseURL, password)
	}
	storeJSONCacheEntry(path, "token", key, token)
}

// viewerCacheKey binds a viewer token's cache entry to its password. It uses
// the CHAP KDF salted with the URL, so the cache file is no better an offline
// oracle for the password than a sniffed CHAP exchange.
func viewerCacheKey(baseURL, password string) string {
	url := apiclient.NormalizeBaseURL(baseURL)
	return url + " viewer " + chap.Response(password, url)
}
