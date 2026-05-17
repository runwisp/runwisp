// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoveLogFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "test.log")
	for _, suffix := range []string{"", ".idx", ".meta", ".prev", ".idx.prev", ".tidx", ".tidx.prev"} {
		assert.NoError(t, os.WriteFile(base+suffix, []byte("x"), 0644))
	}
	RemoveLogFiles(base)
	for _, suffix := range []string{"", ".idx", ".meta", ".prev", ".idx.prev", ".tidx", ".tidx.prev"} {
		_, err := os.Stat(base + suffix)
		assert.True(t, os.IsNotExist(err), "expected %s to be removed", base+suffix)
	}
}

func TestRemoveEmptyParents(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "task", "sub")
	assert.NoError(t, os.MkdirAll(nested, 0755))
	logFile := filepath.Join(nested, "test.log")

	RemoveEmptyParents(logFile, root)

	_, err := os.Stat(nested)
	assert.True(t, os.IsNotExist(err), "empty nested dir should be removed")
	_, err = os.Stat(filepath.Join(root, "task"))
	assert.True(t, os.IsNotExist(err), "empty task dir should be removed")
	_, err = os.Stat(root)
	assert.NoError(t, err, "root dir should still exist")
}

func TestCountTailLines_NonexistentFile(t *testing.T) {
	got := CountTailLines("/nonexistent/path/run.log")
	assert.Equal(t, int64(0), got)
}

func TestCountTailLines_WithLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")

	// Write 5 lines directly — no rotation, no index needed for a small file.
	content := "line1\nline2\nline3\nline4\nline5\n"
	require.NoError(t, os.WriteFile(logPath, []byte(content), 0644))

	got := CountTailLines(logPath)
	assert.Equal(t, int64(5), got)
}
