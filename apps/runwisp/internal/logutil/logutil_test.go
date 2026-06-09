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

// TestWriteLogMeta_RoundTripsViaReadLogMeta covers the happy path of
// WriteLogMeta + ReadLogMeta, and incidentally proves the file is created at
// the .meta sidecar path next to the log.
func TestWriteLogMeta_RoundTripsViaReadLogMeta(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	meta := LogMeta{RotatedLines: 42, RotatedBytes: 4096, FinalLines: 10, Finalized: true}
	WriteLogMeta(logPath, meta)

	got := ReadLogMeta(logPath)
	assert.Equal(t, meta, got)
}

// TestWriteLogMeta_FailedWriteDoesNotPanic exercises the os.WriteFile error
// branch by pointing the log path inside a non-existent directory. The
// function should swallow the error and log a warning rather than panic.
func TestWriteLogMeta_FailedWriteDoesNotPanic(t *testing.T) {
	// Build a path whose parent directory does not exist — WriteFile rejects it.
	bogus := filepath.Join(t.TempDir(), "nonexistent-dir", "run.log")
	WriteLogMeta(bogus, LogMeta{RotatedLines: 1})
	// Nothing to assert beyond "didn't panic" — slog warning is fire-and-forget.
}

// TestReadLogMeta_CorruptFileReturnsZeroValue exercises the json.Unmarshal
// failure branch by writing garbage bytes to the meta path.
func TestReadLogMeta_CorruptFileReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	require.NoError(t, os.WriteFile(MetaPath(logPath), []byte("not-json"), 0644))

	got := ReadLogMeta(logPath)
	assert.Equal(t, LogMeta{}, got, "corrupt meta must yield zero value")
}
