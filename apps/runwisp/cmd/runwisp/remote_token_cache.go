// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
)

// The CLI caches the JWT minted by a remote daemon so repeated `runwisp exec
// --url` invocations reuse one session instead of re-running CHAP each time.
// Re-handshaking would burn two hits (challenge + login) against the daemon's
// per-IP auth rate limit on every call; reusing the 24h JWT keeps a script
// that triggers often well under that ceiling. The cache is a best-effort
// optimization: a stale or unreadable entry simply falls through to a fresh
// handshake, and the trigger path re-authenticates on a 401 regardless.

// cacheSkew trims a margin off a token's expiry so we re-handshake slightly
// early rather than send a JWT the daemon is about to reject.
const cacheSkew = 60 * time.Second

type cachedToken struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"` // unix seconds; 0 means "unknown"
}

// tokenCachePath returns the per-user cache file path. It lives under the OS
// cache dir, not the daemon's --data dir: a remote client has no data dir of
// its own, and the file is keyed by daemon URL so one CLI can hold sessions
// for several daemons.
func tokenCachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runwisp", "tokens.json"), nil
}

// loadCachedToken returns a non-expired cached JWT for baseURL, or "" when
// none is usable. Any error (missing file, corrupt JSON, no cache dir) yields
// "" so the caller falls through to a fresh handshake.
func loadCachedToken(baseURL string) string {
	path, err := tokenCachePath()
	if err != nil {
		return ""
	}
	cache := loadJSONCacheMap[cachedToken](path)
	entry, ok := cache[apiclient.NormalizeBaseURL(baseURL)]
	if !ok || entry.Token == "" {
		return ""
	}
	if entry.ExpiresAt != 0 && time.Now().Add(cacheSkew).Unix() >= entry.ExpiresAt {
		return ""
	}
	return entry.Token
}

// storeCachedToken persists token for baseURL, read-modify-writing the cache
// map. Failures are logged at debug and swallowed — caching is an
// optimization, never a precondition for the trigger.
func storeCachedToken(baseURL, token string) {
	path, err := tokenCachePath()
	if err != nil {
		return
	}
	storeJSONCacheEntry(path, "token", apiclient.NormalizeBaseURL(baseURL), cachedToken{Token: token, ExpiresAt: jwtExpiry(token)})
}

// jwtExpiry pulls the `exp` claim (unix seconds) out of a JWT without
// verifying its signature — the daemon already vouched for the token, we only
// want its lifetime so we can drop it before it goes stale. Returns 0 when the
// claim can't be read; callers treat 0 as "unknown" and lean on 401-retry.
func jwtExpiry(token string) int64 {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return 0
	}
	return claims.Exp
}
