// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveRunLogPath_SameSecondDifferentIDsNeverCollide is the bug-first
// regression for the old 4-char-of-ID suffix: two runs of the same task
// created in the same wall-clock second (concurrent `instances`, or an
// unrelated retrigger a moment after a scheduled fire) used to have a real,
// if rare, chance of landing on the identical path and silently corrupting
// or losing one run's output. The suffix is now the full run ID, so distinct
// IDs can never collide.
func TestResolveRunLogPath_SameSecondDifferentIDsNeverCollide(t *testing.T) {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	// Same last 4 characters, different everywhere else — exactly the case a
	// truncated suffix would have collided on.
	a := ResolveRunLogPath("/var/log", "task1", "01ARZ3NDEKTSV4RRFFQ69G5FAA", createdAt)
	b := ResolveRunLogPath("/var/log", "task1", "01BRZ3NDEKTSV4RRFFQ69G5FAA", createdAt)
	assert.NotEqual(t, a, b, "distinct run IDs sharing a suffix in the same second must resolve to distinct paths")
}

func TestRemoveLogFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "test.log")
	paths := []string{base, MetaPath(base), PrevPath(base)}
	for _, p := range paths {
		assert.NoError(t, os.WriteFile(p, []byte("x"), 0644))
	}
	RemoveLogFiles(base)
	for _, p := range paths {
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "expected %s to be removed", p)
	}
}

func TestMetaAndPrevPathsAreHidden(t *testing.T) {
	logPath := "/var/log/task/20240615_143022_a1b2.log"
	assert.Equal(t, "/var/log/task/.20240615_143022_a1b2.log.meta", MetaPath(logPath))
	assert.Equal(t, "/var/log/task/.20240615_143022_a1b2.log.prev", PrevPath(logPath))
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

// TestReadLogMeta_RoundTripsViaContainer writes a metadata record into the
// container and reads it back through ReadLogMeta.
func TestReadLogMeta_RoundTripsViaContainer(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	meta := LogMeta{RotatedLines: 42, RotatedBytes: 4096, FinalLines: 10, Finalized: true}
	require.NoError(t, os.WriteFile(MetaPath(logPath), MetaRecord(meta), 0644))

	got := ReadLogMeta(logPath)
	assert.Equal(t, meta, got)
}

// TestReadLogMeta_MissingContainerReturnsZeroValue covers a run with no
// container yet (no rotation, still in flight).
func TestReadLogMeta_MissingContainerReturnsZeroValue(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	assert.Equal(t, LogMeta{}, ReadLogMeta(logPath))
}

// TestReadLogMeta_CorruptContainerReturnsZeroValue writes garbage bytes to the
// container; the scan stops at the first malformed record.
func TestReadLogMeta_CorruptContainerReturnsZeroValue(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "run.log")
	require.NoError(t, os.WriteFile(MetaPath(logPath), []byte("not-a-valid-record"), 0644))

	assert.Equal(t, LogMeta{}, ReadLogMeta(logPath), "corrupt container must yield zero value")
}
