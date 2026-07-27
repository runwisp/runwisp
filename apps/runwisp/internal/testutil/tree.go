// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// SnapshotTree records every file under root, keyed by its path relative to
// root, with its exact contents as the value. Two snapshots compared with
// assert.Equal answer "did anything on disk change" in bytes rather than in
// timestamps or in the absence of a complaint — which is what a dry run, a
// rolled-back transaction, or a read-only planning step has to prove.
//
// Directories are not recorded, so an empty directory the code created but never
// filled won't show up; assert on that one directly.
func SnapshotTree(t testing.TB, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		snap[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snap
}
