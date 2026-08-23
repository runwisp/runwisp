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
