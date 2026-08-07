// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestOpts(dir string) LogWriterOpts {
	return LogWriterOpts{
		LogPath: filepath.Join(dir, "test.log"),
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
	assert.False(t, w.truncated)
	assert.Greater(t, w.totalProduced, int64(0))
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

	assert.True(t, w.truncated)

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
	assert.True(t, w.truncated)
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
	w.diskCheckInterval = 1 // probe on every write past the first

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
	assert.True(t, w.truncated, "writer must mark the run truncated")
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
	cancelled := false
	opts.CancelFunc = func() { cancelled = true }
	var seenKilled bool
	opts.OnDiskPressure = func(free, min int64, killedTask bool) {
		seenKilled = killedTask
	}

	w, err := NewLogWriter(opts)
	require.NoError(t, err)
	w.diskCheckInterval = 1

	_, err = w.Write([]byte("first\n"))
	require.NoError(t, err)
	_, err = w.Write([]byte("trigger\n"))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	assert.True(t, cancelled, "kill_task must cancel the task context on disk pressure")
	assert.True(t, seenKilled, "OnDiskPressure must report killedTask=true")
	assert.True(t, w.truncated)
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

	assert.True(t, w.truncated)

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "latest output")
	// The rotated-away segment should be cleaned up by Close.
	_, err = os.Stat(logutil.PrevPath(opts.LogPath))
	assert.True(t, os.IsNotExist(err))
	// The container should record rotation info.
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

	assert.False(t, w.truncated)
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

	assert.True(t, w.truncated)

	meta := logutil.ReadLogMeta(opts.LogPath)
	assert.True(t, meta.Finalized)
	assert.Greater(t, meta.RotatedLines, int64(0), "should have rotated away some lines")
	assert.Greater(t, meta.RotatedBytes, int64(0), "should have rotated away some bytes")
	assert.Greater(t, meta.FinalLines, int64(0), "current file should have lines")

	// Total lines should account for all written lines
	totalLines := meta.RotatedLines + meta.FinalLines
	assert.Greater(t, totalLines, int64(0))

	// The container should exist on disk (hidden).
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

// TestLogWriter_OnlyLogVisibleInListing is the regression guard for the whole
// sidecar-consolidation change: a run that produces an index AND frame history
// must leave exactly one visible file (the `.log`) plus one hidden container in
// the log directory — never the old `.idx`/`.tidx`/`.fhist`/`.meta` clutter.
func TestLogWriter_OnlyLogVisibleInListing(t *testing.T) {
	dir := t.TempDir()
	opts := newTestOpts(dir)
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	require.NoError(t, w.WriteFrameHistory(0, [][]string{{"0%"}, {"100%"}}))
	for i := 0; i < 1100; i++ { // cross the index threshold
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var visible, hidden int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			hidden++
			continue
		}
		visible++
		assert.True(t, strings.HasSuffix(e.Name(), ".log"), "only .log files should be visible, got %s", e.Name())
	}
	assert.Equal(t, 1, visible, "exactly one visible file (the .log)")
	assert.Equal(t, 1, hidden, "exactly one hidden file (the consolidated container)")
}

// TestLogWriter_FramesSurviveRotation pins the run-scoped nature of frame
// history: a frame group recorded before a rotation must still be readable
// after, even though the segment-scoped index resets.
func TestLogWriter_FramesSurviveRotation(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 200
	opts.Overflow = "drop_old"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	require.NoError(t, w.WriteFrameHistory(0, [][]string{{"0%"}, {"50%"}}))

	line := strings.Repeat("x", 100) + "\n"
	for i := 0; i < 6; i++ { // force at least one rotation
		w.Write([]byte(line))
	}
	require.NoError(t, w.Close())

	meta := logutil.ReadLogMeta(opts.LogPath)
	require.Greater(t, meta.RotatedLines, int64(0), "test must trigger rotation")

	frames, ok := logutil.ReadFrameHistory(opts.LogPath, 0)
	require.True(t, ok, "frame history must survive rotation")
	assert.Equal(t, [][]string{{"0%"}, {"50%"}}, frames)
}

// --- Timestamp Index Tests ---

// TestLogWriter_ShortRunRecordsNoIndex pins the lazy policy: a run that produces
// fewer than LogIndexInterval lines records no line index in the container (it
// holds only the final metadata record).
func TestLogWriter_ShortRunRecordsNoIndex(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	sc := logutil.ReadSidecar(opts.LogPath)
	assert.Empty(t, sc.Index, "short run must record no line index")
	assert.True(t, sc.Meta.Finalized, "container still holds the final metadata record")
}

// TestLogWriter_IndexAppearsAtThreshold pins the inverse: once the segment
// crosses LogIndexInterval lines the index records appear with their chunk-0
// backfill entry.
func TestLogWriter_IndexAppearsAtThreshold(t *testing.T) {
	opts := newTestOpts(t.TempDir())
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	for i := 0; i < 1100; i++ {
		w.Write([]byte("line\n"))
	}
	require.NoError(t, w.Close())

	sc := logutil.ReadSidecar(opts.LogPath)
	require.NotEmpty(t, sc.Index, "index must exist once the segment crosses the threshold")
	assert.Equal(t, int64(0), sc.Index[0], "chunk-0 backfill must be at offset 0")
}

// TestLogWriter_WriteLineEvent_MonotonicAcrossRotations is the invariant for
// the line-coordinate world: the absolute line number returned by
// WriteLineEvent is strictly monotonic across rotations.
//
// Note: rotations themselves write a "[SYSTEM] Log rotated:…" line that
// occupies a real line number on disk, so consecutive WriteLineEvent calls can
// have a gap of one across a rotation boundary. The invariant is
// strict-monotonicity, not consecutive numbering. A regression here corrupts
// every downstream cursor (TUI, web UI, cloud replay).
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

	// The next line number that would be issued must not exceed the total
	// recorded lines.
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

// TestLogWriter_InjectedClockStampsSystemLine pins the determinism guarantee of
// the LogWriter clock injection: the SYSTEM line written on truncation must
// format its timestamp from the injected Now, not an inline time.Now(). A
// regression here would re-introduce a hidden wall-clock read inside the log
// path.
func TestLogWriter_InjectedClockStampsSystemLine(t *testing.T) {
	fixed := time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC)
	opts := newTestOpts(t.TempDir())
	opts.MaxSize = 100
	opts.Overflow = "drop_new"
	opts.Now = func() time.Time { return fixed }

	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	w.Write([]byte(strings.Repeat("x", 110) + "\n"))
	require.NoError(t, w.Close())

	data, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	want := fixed.Format("2006-01-02 15:04:05")
	assert.Contains(t, string(data), want,
		"SYSTEM line must use the injected clock's formatted timestamp")
}
