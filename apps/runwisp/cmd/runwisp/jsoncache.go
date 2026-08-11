// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"log/slog"

	"github.com/runwisp/runwisp/internal/datadir"
)

// runwispCacheFile returns the path to name under the per-user OS cache dir's
// "runwisp" subdirectory — the shared home for the pin store and token cache,
// neither of which lives in a --data dir (a remote client has none of its own).
func runwispCacheFile(name string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "runwisp", name), nil
}

// loadJSONCacheMap reads path as a JSON object into a map, tolerating a
// missing or corrupt file by returning an empty map — callers treat "no
// entry" and "unreadable cache" identically.
func loadJSONCacheMap[V any](path string) map[string]V {
	cache := map[string]V{}
	data, err := os.ReadFile(path)
	if err != nil {
		return cache
	}
	_ = json.Unmarshal(data, &cache)
	return cache
}

// storeJSONCacheEntry read-modify-writes entry under key into the JSON object
// cache at path. Failures are logged at debug and swallowed: these caches are
// best-effort optimizations, never a precondition for the caller's operation.
func storeJSONCacheEntry[V any](path, what, key string, entry V) {
	cache := loadJSONCacheMap[V](path)
	cache[key] = entry

	data, err := json.Marshal(cache)
	if err != nil {
		slog.Debug("Skipping "+what+" cache: marshal failed", "err", err)
		return
	}
	if err := datadir.EnsureDir(filepath.Dir(path)); err != nil {
		slog.Debug("Skipping "+what+" cache: cannot create cache dir", "err", err)
		return
	}
	if err := datadir.WriteSecretFile(path, data); err != nil {
		slog.Debug("Skipping "+what+" cache: write failed", "err", err)
	}
}
