// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureLogEvents subscribes to EventLogLine and returns a synchronized
// accumulator the caller can read after the stream has finished.
func captureLogEvents(t *testing.T, eb events.EventBus) (drain func() []events.LogLineEvent) {
	t.Helper()
	var (
		mu   sync.Mutex
		seen []events.LogLineEvent
	)
	unsub := eb.Subscribe(events.EventLogLine, func(e events.Event) {
		le, ok := e.Data.(events.LogLineEvent)
		if !ok {
			return
		}
		mu.Lock()
		seen = append(seen, le)
		mu.Unlock()
	})
	t.Cleanup(unsub)
	return func() []events.LogLineEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]events.LogLineEvent, len(seen))
		copy(out, seen)
		return out
	}
}

// TestStreamManager_OneEventPerLine asserts that the new bus shape emits one
// LogLineEvent per source line — no batching — and that each event's LineNum
// matches the absolute number assigned by LogWriter (0..N-1 here).
func TestStreamManager_OneEventPerLine(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	w, err := NewLogWriter(LogWriterOpts{
		LogPath: logPath,
	})
	require.NoError(t, err)

	eb := events.NewEventBus()
	drain := captureLogEvents(t, eb)
	sm := &RoutingExecutor{eventBus: eb}

	task := &model.Task{Name: "t"}
	run := &model.Run{ID: "r1"}

	source := strings.NewReader("alpha\nbeta\ngamma\n")
	sm.streamToFile(source, w, task, run, logutil.StreamStdout)
	require.NoError(t, w.Close())

	got := drain()
	require.Len(t, got, 3)
	for i, ev := range got {
		assert.Equal(t, int64(i), ev.LineNum, "event %d line num", i)
		assert.Equal(t, "stdout", ev.Stream)
		assert.False(t, ev.Continued)
		assert.NotContains(t, ev.Text, "\n")
	}
	assert.Equal(t, "alpha", got[0].Text)
	assert.Equal(t, "beta", got[1].Text)
	assert.Equal(t, "gamma", got[2].Text)
}

// TestStreamManager_StderrTaggedNoPrefixInText covers the plan invariant:
// the on-disk line keeps the "[ERR] " prefix for `cat run.log` ergonomics,
// but events carry stream:"stderr" and Text WITHOUT that prefix so live
// renderers can style it without parsing.
func TestStreamManager_StderrTaggedNoPrefixInText(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	w, err := NewLogWriter(LogWriterOpts{
		LogPath: logPath,
	})
	require.NoError(t, err)

	eb := events.NewEventBus()
	drain := captureLogEvents(t, eb)
	sm := &RoutingExecutor{eventBus: eb}

	task := &model.Task{Name: "t"}
	run := &model.Run{ID: "r1"}

	sm.streamToFile(strings.NewReader("uh oh\n"), w, task, run, logutil.StreamStderr)
	require.NoError(t, w.Close())

	got := drain()
	require.Len(t, got, 1)
	assert.Equal(t, "stderr", got[0].Stream)
	assert.Equal(t, "uh oh", got[0].Text)
	assert.NotContains(t, got[0].Text, "[ERR]")

	// On-disk: prefix stays.
	disk, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(disk), "[ERR] uh oh\n")
}

// TestStreamManager_OversizedLineSplit exercises LineBuffer's >64KB split:
// the logical line shows up as multiple events with monotonic LineNums, and
// segments 2..N carry Continued=true.
func TestStreamManager_OversizedLineSplit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "test.log")
	w, err := NewLogWriter(LogWriterOpts{
		LogPath: logPath,
	})
	require.NoError(t, err)

	eb := events.NewEventBus()
	drain := captureLogEvents(t, eb)
	sm := &RoutingExecutor{eventBus: eb}

	task := &model.Task{Name: "t"}
	run := &model.Run{ID: "r1"}

	// 200 KB of one logical line, no newline until the very end → forces
	// LineBuffer overflow flushes (each ~64KB).
	huge := strings.Repeat("x", 200*1024) + "\n"
	sm.streamToFile(strings.NewReader(huge), w, task, run, logutil.StreamStdout)
	require.NoError(t, w.Close())

	got := drain()
	require.GreaterOrEqual(t, len(got), 2, "200KB without newline should split into multiple segments")

	// First segment is not a continuation; subsequent are.
	assert.False(t, got[0].Continued, "first segment must not be flagged continued")
	for i := 1; i < len(got); i++ {
		assert.True(t, got[i].Continued, "segment %d must be continued=true", i)
	}

	for i := 1; i < len(got); i++ {
		assert.Equal(t, got[i-1].LineNum+1, got[i].LineNum)
	}
}
