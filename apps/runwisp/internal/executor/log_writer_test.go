// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOpts(dir string) LogWriterOpts {
	logPath := filepath.Join(dir, "test.log")
	return LogWriterOpts{
		LogPath:  logPath,
		IdxPath:  logPath + ".idx",
		TidxPath: logPath + ".tidx",
	}
}

func TestLogWriter_BasicWrite(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	_, err = w.Write([]byte("hello\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "hello")
	assert.False(t, w.Truncated())
}

func TestLogWriter_HeadOverflow(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "head"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	chunk := strings.Repeat("x", 50) + "\n"
	w.Write([]byte(chunk))
	w.Write([]byte(chunk)) // should trigger truncation
	w.Write([]byte("this should be silently dropped\n"))
	require.NoError(t, w.Close())

	assert.True(t, w.Truncated())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "silently dropped")
	assert.Contains(t, string(data), "SYSTEM")
}

func TestLogWriter_KillOverflow(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "kill"
	cancelled := false
	opts.CancelFunc = func() { cancelled = true }
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	chunk := strings.Repeat("x", 110) + "\n"
	w.Write([]byte(chunk))
	require.NoError(t, w.Close())

	assert.True(t, cancelled)
	assert.True(t, w.Truncated())
}

func TestLogWriter_TailRotation(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 200
	opts.Overflow = "tail"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	first := strings.Repeat("a", 100) + "\n"
	w.Write([]byte(first))

	second := strings.Repeat("b", 100) + "\n"
	w.Write([]byte(second))

	third := "latest output\n"
	w.Write([]byte(third))
	require.NoError(t, w.Close())

	assert.True(t, w.Truncated())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "latest output")
	// .prev file should be cleaned up by Close
	_, err = os.Stat(opts.LogPath + ".prev")
	assert.True(t, os.IsNotExist(err))
	// Metadata file should exist with rotation info
	meta := logutil.ReadLogMeta(opts.LogPath)
	assert.True(t, meta.Finalized)
	assert.Greater(t, meta.RotatedLines, int64(0))
	assert.Greater(t, meta.RotatedBytes, int64(0))
	assert.Greater(t, meta.FinalLines, int64(0))
}

func TestLogWriter_UnlimitedSize(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 0
	opts.Overflow = "tail"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	bigChunk := strings.Repeat("x", 10000) + "\n"
	for i := 0; i < 10; i++ {
		w.Write([]byte(bigChunk))
	}
	require.NoError(t, w.Close())

	assert.False(t, w.Truncated())
	info, err := os.Stat(opts.LogPath)
	require.NoError(t, err)
	assert.Greater(t, info.Size(), int64(90000))
}

func TestLogWriter_MultipleRotationsMeta(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 300
	opts.Overflow = "tail"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// Write enough to trigger multiple rotations
	line := strings.Repeat("x", 140) + "\n" // 141 bytes per line
	for i := 0; i < 20; i++ {
		w.Write([]byte(line))
	}
	require.NoError(t, w.Close())

	assert.True(t, w.Truncated())

	meta := logutil.ReadLogMeta(opts.LogPath)
	assert.True(t, meta.Finalized)
	assert.Greater(t, meta.RotatedLines, int64(0), "should have rotated away some lines")
	assert.Greater(t, meta.RotatedBytes, int64(0), "should have rotated away some bytes")
	assert.Greater(t, meta.FinalLines, int64(0), "current file should have lines")

	// Total lines should account for all written lines
	totalLines := meta.RotatedLines + meta.FinalLines
	assert.Greater(t, totalLines, int64(0))

	// .meta file should exist on disk
	_, err = os.Stat(logutil.MetaPath(opts.LogPath))
	assert.NoError(t, err)
}

func TestLogWriter_NoRotationMeta(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	w.Write([]byte("line1\n"))
	w.Write([]byte("line2\n"))
	require.NoError(t, w.Close())

	meta := logutil.ReadLogMeta(opts.LogPath)
	assert.True(t, meta.Finalized)
	assert.Equal(t, int64(0), meta.RotatedLines)
	assert.Equal(t, int64(0), meta.RotatedBytes)
	assert.Equal(t, int64(2), meta.FinalLines)
}

func TestReadLogMeta_MissingFile(t *testing.T) {
	meta := logutil.ReadLogMeta("/nonexistent/path.log")
	assert.Equal(t, int64(0), meta.RotatedLines)
	assert.Equal(t, int64(0), meta.RotatedBytes)
	assert.False(t, meta.Finalized)
}

// --- Timestamp Index Tests ---

func TestLogWriter_TimestampIndex_BasicEntries(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// Write 2048+ lines to trigger at least 2 line-based tidx entries (at 0 and 1024)
	for i := 0; i < 2050; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)

	// At minimum: entry at line 0, line 1024, line 2048, plus final entry on Close
	assert.GreaterOrEqual(t, len(entries), 3)
	assert.Equal(t, uint32(0), entries[0].Line)
	assert.Greater(t, entries[0].Timestamp, int64(0))

	// Entries should have monotonically non-decreasing line numbers
	for i := 1; i < len(entries); i++ {
		assert.GreaterOrEqual(t, entries[i].Line, entries[i-1].Line)
		assert.GreaterOrEqual(t, entries[i].Timestamp, entries[i-1].Timestamp)
	}
}

func TestLogWriter_TimestampIndex_TimeBasedEntries(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// Inject a fake clock that advances 1500ms per call
	var fakeTime atomic.Int64
	fakeTime.Store(1000000)
	w.nowMs = func() int64 {
		return fakeTime.Add(1500)
	}

	// Write only 10 lines (far fewer than LogIndexInterval=1024)
	// but the clock advances 1500ms per write, so time trigger should fire
	for i := 0; i < 10; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)

	// First entry at line 0 (line-interval trigger) + time-triggered entries + final
	// With 10 lines and 1500ms gaps, every write triggers (since >= 1000ms)
	assert.GreaterOrEqual(t, len(entries), 10)
}

func TestLogWriter_TimestampIndex_Rotation(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 200
	opts.Overflow = "tail"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	first := strings.Repeat("a", 100) + "\n"
	w.Write([]byte(first))
	second := strings.Repeat("b", 100) + "\n"
	w.Write([]byte(second))
	third := "latest output\n"
	w.Write([]byte(third))
	require.NoError(t, w.Close())

	// .tidx should exist with entries for the current (post-rotation) segment
	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	assert.Greater(t, len(entries), 0)

	// .tidx.prev should be cleaned up
	_, err = os.Stat(opts.TidxPath + ".prev")
	assert.True(t, os.IsNotExist(err))
}

func TestLogWriter_TimestampIndex_FinalEntryOnClose(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	w.Write([]byte("line1\n"))
	w.Write([]byte("line2\n"))
	w.Write([]byte("line3\n"))
	require.NoError(t, w.Close())

	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	require.Greater(t, len(entries), 0)

	// Last entry should have the total line count
	last := entries[len(entries)-1]
	assert.Equal(t, uint32(3), last.Line)
}

func TestLogWriter_TimestampIndex_LookupUseCases(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// Inject controlled timestamps: each line gets a known timestamp
	var fakeTime atomic.Int64
	baseTime := int64(1700000000000) // 2023-11-14 ~22:13 UTC
	fakeTime.Store(baseTime)
	w.nowMs = func() int64 {
		// Advance 2 seconds per call to ensure every line triggers a tidx entry
		return fakeTime.Add(2000)
	}

	// Write 50 lines, each ~2 seconds apart
	for i := 0; i < 50; i++ {
		w.Write([]byte("log line\n"))
	}
	require.NoError(t, w.Close())

	// Read the tidx for lookup tests
	f, err := os.Open(opts.TidxPath)
	require.NoError(t, err)
	defer f.Close()
	info, err := f.Stat()
	require.NoError(t, err)
	size := info.Size()

	// Use case 1: "what time is line 25?"
	ts, err := logutil.LookupTimestampByLine(f, size, 25)
	require.NoError(t, err)
	assert.Greater(t, ts, baseTime)
	assert.Less(t, ts, baseTime+200000) // within reasonable range

	// Use case 2: "show me lines between T+20s and T+60s"
	fromMs := baseTime + 20000
	toMs := baseTime + 60000
	startLine, endLine, err := logutil.LookupLineRangeByTime(f, size, fromMs, toMs)
	require.NoError(t, err)
	assert.Greater(t, endLine, startLine)
	assert.Less(t, startLine, uint32(50))
}

func TestLogWriter_TimestampIndex_NoTidxPath(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	w, err := NewLogWriter(LogWriterOpts{
		LogPath: logPath,
		IdxPath: logPath + ".idx",
		// TidxPath intentionally empty
	})
	require.NoError(t, err)

	w.Write([]byte("line\n"))
	require.NoError(t, w.Close())

	// No .tidx file should exist
	_, err = os.Stat(logPath + ".tidx")
	assert.True(t, os.IsNotExist(err))
}
