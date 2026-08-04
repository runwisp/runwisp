// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/require"
)

// These tests write real files under t.TempDir(). That is deliberate rather than
// a shortcut: the whole subject here is on-disk behavior — temp+rename, rollback
// to byte-identical pre-images, and a gate that runs config.Load against what
// actually landed. A fake filesystem would assert the mock, not the guarantee.

// writeFileTree stages files (keys may contain subdirs) under a fresh temp dir
// and returns the dir. The root config is conventionally "runwisp.toml".
func writeFileTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o600))
	}
	return dir
}

// readFile returns a file's contents, failing the test if it can't be read.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// taskNames returns every task/service name in a loaded config.
func taskNames(cfg *config.Config) []string {
	names := make([]string, 0, len(cfg.Tasks))
	for i := range cfg.Tasks {
		names = append(names, cfg.Tasks[i].Name)
	}
	return names
}

// findTask returns the named task, failing the test when it's absent.
func findTask(t *testing.T, cfg *config.Config, name string) *model.Task {
	t.Helper()
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Name == name {
			return &cfg.Tasks[i]
		}
	}
	t.Fatalf("task %q not found in config (have %v)", name, taskNames(cfg))
	return nil
}
