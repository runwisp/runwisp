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
// globs / bootMatched let Stale re-evaluate the include patterns: a file newly
// dropped into (or removed from) a watched conf.d/ flips stale even though it
// wasn't hashed at boot.
type Snapshot struct {
	mu          sync.Mutex
	files       []fileDigest
	root        string
	globs       []string
	bootMatched []string
	loadedAt    time.Time
}

// NewSnapshot hashes the config file at path, every included TOML file, and
// every env_file the loaded cfg references (each already resolved against its
// declaring file's dir in collectWatchFiles). now is injected so callers
// control the clock.
func NewSnapshot(path string, cfg *Config, now time.Time) *Snapshot {
	files, root, globs, bootMatched := snapshotInputs(path, cfg)
	return &Snapshot{files: files, root: root, globs: globs, bootMatched: bootMatched, loadedAt: now}
}

// Refresh re-pins the snapshot to the config inputs as they are now, marking
// `now` as the new load time. Called after a successful reload so config_stale
// reflects the newly-live config rather than the boot-time one.
func (s *Snapshot) Refresh(path string, cfg *Config, now time.Time) {
	files, root, globs, bootMatched := snapshotInputs(path, cfg)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = files
	s.root = root
	s.globs = globs
	s.bootMatched = bootMatched
	s.loadedAt = now
}

// snapshotInputs hashes runwisp.toml plus every included TOML file and
// referenced env_file, and returns the include globs / boot-matched set so
// Stale can re-evaluate the patterns later.
func snapshotInputs(path string, cfg *Config) (files []fileDigest, root string, globs, bootMatched []string) {
	paths := []string{path}
	root = path
	if abs, err := filepath.Abs(path); err == nil {
		root = abs
	}
	if cfg != nil {
		paths = append(paths, cfg.watchFiles...)
		globs = cfg.includeGlobs
		bootMatched = append([]string(nil), cfg.includeFiles...)
		sort.Strings(bootMatched)
	}

	seen := make(map[string]struct{}, len(paths))
	files = make([]fileDigest, 0, len(paths))
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		files = append(files, digestFile(p))
	}
	return files, root, globs, bootMatched
}

// LoadedAt reports when the snapshot was taken (i.e. when this config
// became the daemon's live task set).
func (s *Snapshot) LoadedAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadedAt
}

// Stale re-reads every watched file and reports whether any differs from the
// last pin — edited content, a deleted file, or a file that newly appeared all
// count. It also re-evaluates the include globs so a file freshly added to (or
// removed from) a watched conf.d/ counts even though it was never hashed. It is
// called per /api/info request rather than from a watcher, so the answer is
// always current and the daemon never reacts on its own.
func (s *Snapshot) Stale() bool {
	s.mu.Lock()
	files := s.files
	globs := s.globs
	root := s.root
	bootMatched := s.bootMatched
	s.mu.Unlock()
	for _, f := range files {
		if digestFile(f.path) != f {
			return true
		}
	}
	if len(globs) > 0 && !slices.Equal(globMatches(globs, root), bootMatched) {
		return true
	}
	return false
}

// globMatches expands the resolved include patterns and returns the
// deduplicated, lexically sorted set of matched files, mirroring how
// resolveIncludes computed bootMatched. A bad pattern yields no matches rather
// than an error — Stale must never panic on a config edit.
func globMatches(globs []string, root string) []string {
	seen := make(map[string]struct{})
	var matched []string
	for _, g := range globs {
		hits, err := filepath.Glob(g)
		if err != nil {
			continue
		}
		for _, h := range hits {
			if abs, err := filepath.Abs(h); err == nil {
				h = abs
			}
			if h == root {
				continue
			}
			if _, dup := seen[h]; dup {
				continue
			}
			seen[h] = struct{}{}
			matched = append(matched, h)
		}
	}
	sort.Strings(matched)
	return matched
}

func resolveAgainst(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
