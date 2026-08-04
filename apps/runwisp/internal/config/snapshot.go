// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"
)

// fileDigest pins one watched file's identity: its content hash, or the fact
// that it didn't exist. Tracking absence explicitly means a file that appears
// later (e.g. an env_file created after boot) reads as a change, not a no-op.
type fileDigest struct {
	path   string
	exists bool
	sum    [sha256.Size]byte
}

func digestFile(path string) fileDigest {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileDigest{path: path}
	}
	return fileDigest{path: path, exists: true, sum: sha256.Sum256(data)}
}

// Snapshot records the on-disk identity of every config input — runwisp.toml,
// each [daemon].include file, and each referenced env_file — at the moment a
// config became the daemon's live set. It lets the daemon *tell* the operator
// when on-disk edits aren't live yet (config_stale). An explicit `runwisp
// reload` re-pins it via Refresh so the freshly-applied config no longer reads
// as stale. The mutex guards against Stale (per /api/info request) racing a
// concurrent Refresh.
//
// The glob/bootMatched pairs let Stale re-evaluate the include patterns: a file
// newly dropped into (or removed from) a watched conf.d/ flips stale even though
// it wasn't hashed at boot.
type Snapshot struct {
	mu       sync.Mutex
	pins     snapshotPins
	loadedAt time.Time
}

// snapshotPins is the on-disk identity a snapshot was taken of. The two glob
// sets are kept apart because they are expanded by different rules: an
// include_cron glob only ever reads what crond itself would read
// (partitionCrondEligible), and re-globbing it with a plain filepath.Glob is why
// /etc/cron.d/.placeholder — a file the Debian cron package installs on every box
// — made `status` report "config changed" forever after an untouched take-over.
type snapshotPins struct {
	files []fileDigest
	root  string
	// globs are the [daemon].include patterns and bootMatched the files they
	// resolved to at load time.
	globs       []string
	bootMatched []string
	// cronGlobs are the [daemon].include_cron patterns and bootCron the crontabs
	// they resolved to, crond-eligibility filter already applied.
	cronGlobs []string
	bootCron  []string
}

// NewSnapshot hashes the config file at path, every included TOML file, and
// every env_file the loaded cfg references (each already resolved against its
// declaring file's dir in collectWatchFiles). now is injected so callers
// control the clock.
func NewSnapshot(path string, cfg *Config, now time.Time) *Snapshot {
	return &Snapshot{pins: snapshotInputs(path, cfg), loadedAt: now}
}

// Refresh re-pins the snapshot to the config inputs as they are now, marking
// `now` as the new load time. Called after a successful reload so config_stale
// reflects the newly-live config rather than the boot-time one.
func (s *Snapshot) Refresh(path string, cfg *Config, now time.Time) {
	pins := snapshotInputs(path, cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins = pins
	s.loadedAt = now
}

// snapshotInputs hashes runwisp.toml plus every included TOML file and
// referenced env_file, and records the include globs and what they matched so
// Stale can re-evaluate the patterns later.
func snapshotInputs(path string, cfg *Config) snapshotPins {
	var pins snapshotPins
	paths := []string{path}
	pins.root = path
	if abs, err := filepath.Abs(path); err == nil {
		pins.root = abs
	}
	if cfg != nil {
		paths = append(paths, cfg.watchFiles...)
		pins.globs = cfg.includeGlobs
		pins.bootMatched = slices.Sorted(slices.Values(cfg.includeFiles))
		pins.cronGlobs = cfg.cronGlobs
		pins.bootCron = slices.Sorted(slices.Values(cfg.cronMatched))
	}

	seen := make(map[string]struct{}, len(paths))
	pins.files = make([]fileDigest, 0, len(paths))
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		pins.files = append(pins.files, digestFile(p))
	}
	return pins
}

// LoadedAt reports when the snapshot was taken (i.e. when this config
// became the daemon's live task set).
func (s *Snapshot) LoadedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadedAt
}

// pinned returns the snapshot's pins under the lock. The slices inside are
// replaced wholesale by Refresh, never mutated, so the copy is safe to read
// after the lock is dropped.
func (s *Snapshot) pinned() snapshotPins {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pins
}

// Stale re-reads every watched file and reports whether any differs from the
// last pin — edited content, a deleted file, or a file that newly appeared all
// count. It also re-evaluates the include globs so a file freshly added to (or
// removed from) a watched conf.d/ counts even though it was never hashed. It is
// called per /api/info request rather than from a watcher, so the answer is
// always current and the daemon never reacts on its own.
func (s *Snapshot) Stale() bool {
	pins := s.pinned()
	for _, f := range pins.files {
		if digestFile(f.path) != f {
			return true
		}
	}
	if len(pins.globs) > 0 && !slices.Equal(globMatches(pins.globs, pins.root, false), pins.bootMatched) {
		return true
	}
	if len(pins.cronGlobs) > 0 && !slices.Equal(globMatches(pins.cronGlobs, pins.root, true), pins.bootCron) {
		return true
	}
	return false
}

// globMatches expands the resolved include patterns and returns the
// deduplicated, lexically sorted set of matched files, mirroring how
// resolveIncludes computed bootMatched. A bad pattern yields no matches rather
// than an error — Stale must never panic on a config edit.
//
// crond applies resolveCronIncludes' own eligibility filter, which is what
// makes this comparable with an include_cron boot set: crond skips a
// .placeholder or a .dpkg-old, so the loader skips them too, and a plain re-glob
// that kept them would differ from the boot set forever. The filter is gated on
// hasGlobMeta for the same reason there: a path the operator typed out is read
// whatever it is called.
func globMatches(globs []string, root string, crond bool) []string {
	seen := make(map[string]struct{})
	var matched []string
	for _, g := range globs {
		matched = appendGlobHits(matched, globHits(g, crond), root, seen)
	}
	sort.Strings(matched)
	return matched
}

// globHits expands one pattern the way the loader that recorded it did.
func globHits(pattern string, crond bool) []string {
	hits, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	if crond && hasGlobMeta(pattern) {
		hits, _ = partitionCrondEligible(hits, nil)
	}
	return hits
}

func resolveAgainst(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
