// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"math"
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

func TestLogWriter_DropNewOverflow(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "drop_new"
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

func TestLogWriter_KillTaskOverflow(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "kill_task"
	cancelled := false
	opts.CancelFunc = func() { cancelled = true }
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	chunk := strings.Repeat("x", 110) + "\n"
	w.Write([]byte(chunk))
	require.NoError(t, w.Close())

	assert.True(t, cancelled)
	assert.True(t, w.Truncated())
	assert.True(t, w.KilledByPolicy(),
		"kill_task overflow must flag KilledByPolicy so the runtime records the run as failed, not stopped")
}

func TestLogWriter_DropNewDoesNotMarkKilledByPolicy(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "drop_new"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	w.Write([]byte(strings.Repeat("x", 110) + "\n"))
	require.NoError(t, w.Close())

	assert.False(t, w.KilledByPolicy(),
		"drop_new keeps the process alive — only kill_task should set KilledByPolicy")
}

// TestLogWriter_DiskPressure_DropNew_FiresCallback verifies the disk-pressure
// path on a non-kill_task policy: log writes silently stop, the task keeps
// running (cancelFunc is NOT called), and OnDiskPressure fires exactly once.
func TestLogWriter_DiskPressure_DropNew_FiresCallback(t *testing.T) {
	dir := t.TempDir()
	opts := newTestOpts(dir)
	opts.LogDir = dir
	opts.Overflow = "drop_new"
	opts.MinFreeDisk = math.MaxInt64 // any free space < this trips the threshold
	opts.DiskCheckInterval = 1       // probe on every write past the first
	cancelled := false
	opts.CancelFunc = func() { cancelled = true }
	hits := 0
	var seenFree, seenMin int64
	var seenKilled bool
	opts.OnDiskPressure = func(free, min int64, killedTask bool) {
		hits++
		seenFree = free
		seenMin = min
		seenKilled = killedTask
	}

	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// First write does not trip (currentOffset==0, gap not > interval).
	_, err = w.Write([]byte("first line\n"))
	require.NoError(t, err)
	// Second write must probe and trip.
	_, err = w.Write([]byte("second line — should be dropped\n"))
	require.NoError(t, err)
	// Third write also drops, must NOT re-fire callback.
	_, err = w.Write([]byte("third line\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.False(t, cancelled, "drop_new policy must not kill the task on disk pressure")
	assert.True(t, w.Truncated(), "writer must mark the run truncated")
	assert.Equal(t, 1, hits, "OnDiskPressure must fire exactly once per writer")
	assert.False(t, seenKilled, "killedTask=false for drop_new")
	assert.Equal(t, int64(math.MaxInt64), seenMin)
	assert.GreaterOrEqual(t, seenFree, int64(0))

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "should be dropped")
}

// TestLogWriter_DiskPressure_KillTask_CancelsContext verifies that disk
// pressure on a kill_task task actually kills the task (calls cancelFunc),
// reports killedTask=true to OnDiskPressure, and stops further writes.
func TestLogWriter_DiskPressure_KillTask_CancelsContext(t *testing.T) {
	dir := t.TempDir()
	opts := newTestOpts(dir)
	opts.LogDir = dir
	opts.Overflow = "kill_task"
	opts.MinFreeDisk = math.MaxInt64
	opts.DiskCheckInterval = 1
	cancelled := false
	opts.CancelFunc = func() { cancelled = true }
	var seenKilled bool
	opts.OnDiskPressure = func(free, min int64, killedTask bool) {
		seenKilled = killedTask
	}

	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	_, err = w.Write([]byte("first\n"))
	require.NoError(t, err)
	_, err = w.Write([]byte("trigger\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.True(t, cancelled, "kill_task must cancel the task context on disk pressure")
	assert.True(t, seenKilled, "OnDiskPressure must report killedTask=true")
	assert.True(t, w.Truncated())
}

func TestLogWriter_DropOldRotation(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 200
	opts.Overflow = "drop_old"
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
	opts.Overflow = "drop_old"
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
	opts.Overflow = "drop_old"
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

	// Write 2050 lines to lazy-open the sidecars and accumulate at least
	// three line-based tidx entries (line 0 backfill, line 1024, line 2048).
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

// TestLogWriter_NoSidecarsForShortRun pins down the new lazy policy: a run
// that produces fewer than LogIndexInterval lines must not leave any .idx
// or .tidx file on disk. This keeps the on-disk footprint of the average
// short cron run minimal.
func TestLogWriter_NoSidecarsForShortRun(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	_, err = os.Stat(opts.IdxPath)
	assert.True(t, os.IsNotExist(err), "short run must leave no .idx sidecar")
	_, err = os.Stat(opts.TidxPath)
	assert.True(t, os.IsNotExist(err), "short run must leave no .tidx sidecar")
}

// TestLogWriter_SidecarsAppearAtThreshold pins down the inverse: once the
// segment crosses LogIndexInterval lines the sidecars must exist with their
// chunk-0 backfill entries, even though the writer didn't open them upfront.
func TestLogWriter_SidecarsAppearAtThreshold(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// 1100 lines exceeds the 1024-line threshold by a small margin, enough
	// to lazy-open the sidecars and accumulate the first proper entry.
	for i := 0; i < 1100; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	_, err = os.Stat(opts.IdxPath)
	require.NoError(t, err, ".idx must exist once the segment crosses the threshold")

	tidxEntries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(tidxEntries), 2, "expect chunk-0 backfill plus chunk-1 entry")
	assert.Equal(t, uint32(0), tidxEntries[0].Line, "chunk-0 backfill must be at Line 0")
	assert.Equal(t, uint32(1024), tidxEntries[1].Line, "second entry must be at Line 1024")
}

func TestLogWriter_TimestampIndex_TimeBasedEntriesAfterThreshold(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	var fakeTime atomic.Int64
	fakeTime.Store(1700000000000)
	w.nowMs = func() int64 {
		// Advance 1500ms per write so that, post-threshold, every write
		// crosses the 1-second time-based trigger.
		return fakeTime.Add(1500)
	}

	for i := 0; i < 1100; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	// Backfill at line 0 + line 1024 + every post-threshold line + final on
	// close. We don't pin an exact count — just verify the time trigger
	// fired at least a few times after the threshold opened the sidecar.
	assert.GreaterOrEqual(t, len(entries), 5)
}

func TestLogWriter_TimestampIndex_FinalEntryOnClose(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	// Write past the lazy-open threshold so the close-time entry actually
	// lands in a real .tidx file.
	const total = 1100
	for i := 0; i < total; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	require.Greater(t, len(entries), 0)

	// The last entry on Close records the total line count for the segment.
	last := entries[len(entries)-1]
	assert.Equal(t, uint32(total), last.Line)
}

func TestLogWriter_TimestampIndex_LookupUseCases(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	var fakeTime atomic.Int64
	baseTime := int64(1700000000000)
	fakeTime.Store(baseTime)
	w.nowMs = func() int64 {
		return fakeTime.Add(2000)
	}

	// Run past the 1024-line threshold so .tidx materialises with multiple
	// entries that LookupLineRangeByTime can search through.
	const total = 1500
	for i := 0; i < total; i++ {
		w.Write([]byte("log line\n"))
	}
	require.NoError(t, w.Close())

	f, err := os.Open(opts.TidxPath)
	require.NoError(t, err)
	defer f.Close()
	info, err := f.Stat()
	require.NoError(t, err)
	size := info.Size()

	// Discover the actual timestamp range from the entries on disk so the
	// lookup queries land inside the run's window regardless of how many
	// nowMs() calls writeOneLine ends up making per write.
	entries, err := logutil.ReadTimestampIndex(opts.TidxPath)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 3, "expect chunk-0 backfill plus post-threshold entries")

	// Use case 1: "what time is line 1100?" — pick a line we know exists in
	// the segment. The timestamp must fall inside the run's window.
	ts, err := logutil.LookupTimestampByLine(f, size, 1100)
	require.NoError(t, err)
	firstTs := entries[0].Timestamp
	lastTs := entries[len(entries)-1].Timestamp
	assert.GreaterOrEqual(t, ts, firstTs)
	assert.LessOrEqual(t, ts, lastTs)

	// Use case 2: "show me a window inside the run". Anchor the window in
	// the actual entry timestamps so we know it overlaps real lines.
	fromMs := entries[1].Timestamp
	toMs := entries[len(entries)-1].Timestamp
	startLine, endLine, err := logutil.LookupLineRangeByTime(f, size, fromMs, toMs)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, endLine, startLine)
	assert.Less(t, startLine, uint32(total))
}

// TestLogWriter_WriteLineEvent_MonotonicAcrossRotations is the Phase 1
// invariant for the new line-coordinate world: the absolute line number
// returned by WriteLineEvent is strictly monotonic across rotations.
//
// Note: rotations themselves write a "[SYSTEM] Log rotated:…" line that
// occupies a real line number on disk, so consecutive WriteLineEvent calls
// can have a gap of one across a rotation boundary. The invariant is
// strict-monotonicity, not consecutive numbering, and lineNum always equals
// rotatedLines+lineCount-1 for the line just written. A regression here
// corrupts every downstream cursor (TUI, web UI, cloud replay).
func TestLogWriter_WriteLineEvent_MonotonicAcrossRotations(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 200
	opts.Overflow = "drop_old"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	const total = 30
	assigned := make([]int64, 0, total)
	line := strings.Repeat("x", 50) // ~51 bytes once formatted; forces rotations
	for i := 0; i < total; i++ {
		n, err := w.WriteLineEvent(line, "stdout")
		require.NoError(t, err)
		// drop_old should never silently drop lines (rotation makes room).
		require.GreaterOrEqual(t, n, int64(0), "line %d unexpectedly dropped", i)
		assigned = append(assigned, n)
	}
	require.NoError(t, w.Close())

	for i := 1; i < len(assigned); i++ {
		assert.Greater(t, assigned[i], assigned[i-1],
			"line numbers must be strictly monotonic: assigned[%d]=%d after assigned[%d]=%d",
			i, assigned[i], i-1, assigned[i-1])
	}
	assert.Equal(t, int64(0), assigned[0])

	// At least one rotation must have happened given the maxSize and total
	// payload — that's the test's whole point.
	meta := logutil.ReadLogMeta(opts.LogPath)
	require.Greater(t, meta.RotatedLines, int64(0), "test must trigger rotation")

	// The last assigned number must equal rotatedLines+(finalLineCount-1)
	// where finalLineCount is the total lines on disk in the current segment
	// at the moment of the last WriteLineEvent call. After Close adds nothing
	// for non-truncated runs, but since drop_old marks truncated=true Close
	// writes a "Total process output:" SYSTEM line, so FinalLines reflects
	// state AFTER Close. The key invariant we can assert without that
	// noise: max assigned + 1 (the next line number that would be issued)
	// must not exceed the total recorded lines.
	assert.LessOrEqual(t, assigned[len(assigned)-1]+1, meta.RotatedLines+meta.FinalLines)
}

// TestLogWriter_WriteLineEvent_DropReturnsNegative covers the contract that
// silently-dropped writes (drop_new mode at limit) report n=-1 so the
// StreamManager skips publishing an event for a line that doesn't exist on
// disk.
func TestLogWriter_WriteLineEvent_DropReturnsNegative(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 80
	opts.Overflow = "drop_new"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	first, err := w.WriteLineEvent(strings.Repeat("x", 50), "stdout")
	require.NoError(t, err)
	assert.Equal(t, int64(0), first)

	// Second write pushes past the limit → drop_new path → -1.
	second, err := w.WriteLineEvent(strings.Repeat("y", 50), "stdout")
	require.NoError(t, err)
	assert.Equal(t, int64(-1), second)

	require.NoError(t, w.Close())
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
