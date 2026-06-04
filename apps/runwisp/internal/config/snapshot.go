// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"crypto/sha256"
	"os"
	"path/filepath"
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

// Snapshot records the on-disk identity of every config input — runwisp.toml
// plus each referenced env_file — at daemon boot. Config reload is
// restart-only by design; Snapshot exists so the daemon can *tell* the
// operator their edits aren't live yet, not to act on them.
type Snapshot struct {
	files    []fileDigest
	loadedAt time.Time
}

// NewSnapshot hashes the config file at path and every env_file the loaded
// cfg references (resolved against the config dir, mirroring loadEnvFile).
// now is injected so callers control the clock.
func NewSnapshot(path string, cfg *Config, now time.Time) *Snapshot {
	baseDir := filepath.Dir(path)
	paths := []string{path}
	if cfg != nil {
		if cfg.Defaults.EnvFile != "" {
			paths = append(paths, resolveAgainst(baseDir, cfg.Defaults.EnvFile))
		}
		for i := range cfg.Tasks {
			if ef := cfg.Tasks[i].EnvFile; ef != "" {
				paths = append(paths, resolveAgainst(baseDir, ef))
			}
		}
	}

	seen := make(map[string]struct{}, len(paths))
	files := make([]fileDigest, 0, len(paths))
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

	return &Snapshot{files: files, loadedAt: now}
}

// LoadedAt reports when the snapshot was taken (i.e. when this config
// became the daemon's live task set).
func (s *Snapshot) LoadedAt() time.Time {
	return s.loadedAt
}

// Stale re-reads every watched file and reports whether any differs from
// boot — edited content, a deleted file, or a file that newly appeared all
// count. It is called per /api/info request rather than from a watcher, so
// the answer is always current and the daemon never reacts on its own.
func (s *Snapshot) Stale() bool {
	for _, f := range s.files {
		if digestFile(f.path) != f {
			return true
		}
	}
	return false
}

func resolveAgainst(baseDir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(baseDir, path)
}
