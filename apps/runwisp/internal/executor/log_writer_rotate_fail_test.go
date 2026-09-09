// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogWriter_RotateCreateFailure_StopsInsteadOfDestroyingPrev is the
// bug-first regression for a rotateTail double-failure: a drop_old rotation
// renames the current segment into .prev (succeeds), but the immediately
// following os.Create of the fresh segment fails (e.g. transient ENOSPC).
//
// Before the fix, rotateTail left w.file pointing at a closed handle without
// setting w.stopped, and handleSizeOverflow kept letting writes fall through
// afterward. currentOffset was never reset either, so the very next write
// re-triggered the overflow check and re-entered rotateTail — whose first act
// is an unconditional os.Remove(prevPath). That silently destroyed the
// segment the second rotation had *just* rotated into .prev, with nothing but
// a slog.Error line to show for it: real, already-captured output vanishes
// with no trace in the run's own log. The fix stops the writer the moment the
// post-rotation create fails, so .prev is never touched again.
func TestLogWriter_RotateCreateFailure_StopsInsteadOfDestroyingPrev(t *testing.T) {
	dir := t.TempDir()
	opts := newTestOpts(dir)
	opts.MaxSize = 300
	opts.Overflow = "drop_old"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	var createCalls int
	realCreate := w.createSegment
	w.createSegment = func(path string) (*os.File, error) {
		createCalls++
		if createCalls == 1 {
			// First rotation succeeds normally.
			return realCreate(path)
		}
		// The second rotation's rename has already moved the live segment into
		// .prev by the time this runs; simulate the disk filling up right here.
		return nil, errors.New("simulated ENOSPC creating new segment")
	}

	line := strings.Repeat("x", 140) // ~141 bytes once formatted
	prevPath := logutil.PrevPath(opts.LogPath)

	// Write until the second rotation attempt fires (createCalls == 2) — the
	// one whose create fails. Its rename step succeeds first, so by the time
	// it fails .prev correctly holds the segment that rotation rotated away.
	for i := 0; i < 20 && createCalls < 2; i++ {
		_, err := w.WriteLineEvent(line, logutil.StreamStdout)
		require.NoError(t, err, "WriteLineEvent itself must not surface the rotation error")
	}
	require.Equal(t, 2, createCalls, "second rotation attempt must have run")
	assert.True(t, w.stopped, "writer must stop once the post-rotation create fails")

	prevContentAfterFailedRotation, err := os.ReadFile(prevPath)
	require.NoError(t, err)
	require.NotEmpty(t, prevContentAfterFailedRotation, ".prev must hold the segment the second rotation rotated away")

	// Further writes must be cleanly dropped, not attempt another rotation.
	for i := 0; i < 3; i++ {
		n, err := w.WriteLineEvent(line, logutil.StreamStdout)
		require.NoError(t, err)
		assert.Equal(t, int64(-1), n, "writes after a stopped writer must be dropped, not error into a closed file")
	}
	assert.Equal(t, 2, createCalls, "no further rotation attempts — and therefore no further os.Remove(prevPath) — may occur once stopped")

	prevContentAfterMoreWrites, err := os.ReadFile(prevPath)
	require.NoError(t, err, ".prev must still exist — it must never be removed without a replacement")
	assert.Equal(t, prevContentAfterFailedRotation, prevContentAfterMoreWrites,
		".prev content must survive later write attempts untouched once the writer has stopped")

	// Close() may still report an error closing the already-closed handle from
	// the failed rotation (the sole production caller already discards it via
	// `defer writer.Close()`); it must not panic or corrupt .prev further.
	_ = w.Close()
	prevContentAfterClose, err := os.ReadFile(prevPath)
	require.NoError(t, err)
	assert.Equal(t, prevContentAfterFailedRotation, prevContentAfterClose)
}

// TestLogWriter_RotateRenameFailure_StopsWriterAndSurfacesFailure is the
// bug-first regression for the silent-rotation-failure finding: a drop_old
// rotation whose rename fails, but whose fallback reopen of the original file
// succeeds, used to log a single slog.Error ("continuing without rotation")
// and then fall straight through to Write() on the reopened handle — meaning
// log_max_size stopped being enforced for the rest of the run with nothing
// but a daemon-log line, invisible to anyone watching the run's own log or
// the web UI, to show for it. The fix treats this exactly like a genuine
// write error: stop the writer and leave a SYSTEM line in the run's own log
// marking what happened, instead of growing the file unbounded.
func TestLogWriter_RotateRenameFailure_StopsWriterAndSurfacesFailure(t *testing.T) {
	dir := t.TempDir()
	opts := newTestOpts(dir)
	opts.MaxSize = 200
	opts.Overflow = "drop_old"
	w, err := NewLogWriter(opts)
	require.NoError(t, err)

	_, err = w.WriteLineEvent(strings.Repeat("a", 100), logutil.StreamStdout)
	require.NoError(t, err)
	_, err = w.WriteLineEvent(strings.Repeat("b", 100), logutil.StreamStdout) // triggers the first rotation, which must succeed
	require.NoError(t, err)
	require.False(t, w.stopped, "the first (unforced) rotation must succeed and leave the writer capturing")

	w.renameFile = func(oldpath, newpath string) error {
		return errors.New("simulated rename failure")
	}

	// Attempts a second rotation: the rename fails, but the fallback reopen of
	// the still-present original file succeeds, so the file handle itself
	// stays perfectly writable.
	n, err := w.WriteLineEvent(strings.Repeat("c", 100), logutil.StreamStdout)
	require.NoError(t, err, "a rotation failure is a drop, like a genuine write error, not a caller-visible error")
	assert.Equal(t, int64(-1), n, "the write that triggered the failed rotation must be dropped, not written past the cap")
	assert.True(t, w.stopped, "log_max_size can no longer be enforced once rotation fails, so the writer must stop instead of growing the file unbounded")
	assert.True(t, w.truncated)

	// Further writes must stay dropped, not resume just because the file
	// handle happens to still be open and writable.
	n2, err2 := w.WriteLineEvent(strings.Repeat("d", 100), logutil.StreamStdout)
	require.NoError(t, err2)
	assert.Equal(t, int64(-1), n2)

	require.NoError(t, w.Close())

	logContent, err := os.ReadFile(opts.LogPath)
	require.NoError(t, err)
	assert.Contains(t, string(logContent), "[SYSTEM]",
		"the rotation failure must be visible inline in the run's own log, not just the daemon's slog output")
	assert.Contains(t, string(logContent), "rotation failed",
		"the SYSTEM line must say what happened, mirroring the genuine write-error and disk-pressure messages")
	assert.NotContains(t, string(logContent), "ddddddddd",
		"output written after the writer stopped must never reach the log file")
}
