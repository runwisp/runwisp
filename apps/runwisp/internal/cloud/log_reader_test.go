// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testRunID = "01HZXXXXXXXXXXXXXXXXXXXXXX"

// writeRunLogRecords creates the on-disk log file for a run at the path
// readExecutionLogReplay resolves to. Each entry is encoded with FormatLine
// so the parser produces the expected (stream, text) pair.
func writeRunLogRecords(t *testing.T, logDir string, run *model.Run, entries []logutil.LogLineRecord) {
	t.Helper()
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))

	var body []byte
	for _, e := range entries {
		body = append(body, logutil.FormatLine(e.Text, e.Stream)...)
	}
	require.NoError(t, os.WriteFile(logPath, body, 0o644))
}

func TestReadExecutionLogReplay_NilRun(t *testing.T) {
	items, final, err := readExecutionLogReplay(nil, "/tmp", 0, 0)
	require.NoError(t, err)
	assert.False(t, final, "unknown run must not be final — the dispatch may not have arrived yet")
	assert.Nil(t, items)
}

func TestReadExecutionLogReplay_NoLogFileTerminalRun_FinalTrue(t *testing.T) {
	reason := model.ReasonSuccess
	run := &model.Run{
		ID:        testRunID,
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: &reason,
		CreatedAt: time.Now(),
	}
	items, final, err := readExecutionLogReplay(run, t.TempDir(), 0, 10)
	require.NoError(t, err)
	assert.True(t, final, "terminal run with no log file must be final")
	assert.Empty(t, items)
}

func TestReadExecutionLogReplay_NoLogFileRunningRun_FinalFalse(t *testing.T) {
	run := &model.Run{
		ID:        testRunID,
		TaskName:  "t1",
		Status:    model.PhaseRunning,
		CreatedAt: time.Now(),
	}
	_, final, err := readExecutionLogReplay(run, t.TempDir(), 0, 10)
	require.NoError(t, err)
	assert.False(t, final, "running run must not be final")
}

func TestReadExecutionLogReplay_ReadsAllLines(t *testing.T) {
	reason := model.ReasonSuccess
	run := &model.Run{
		ID:        testRunID,
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: &reason,
		CreatedAt: time.Now(),
	}
	dir := t.TempDir()
	writeRunLogRecords(t, dir, run, []logutil.LogLineRecord{
		{Stream: logutil.StreamStdout, Text: "first"},
		{Stream: logutil.StreamStderr, Text: "boom"},
		{Stream: logutil.StreamStdout, Text: "third"},
	})

	items, final, err := readExecutionLogReplay(run, dir, 0, 10)
	require.NoError(t, err)
	require.Len(t, items, 3)

	assert.Equal(t, int64(0), items[0].N)
	require.NotNil(t, items[0].Stream)
	assert.Equal(t, protocol.LinesItemStreamStdout, *items[0].Stream)
	assert.Equal(t, "first", items[0].Text)

	require.NotNil(t, items[1].Stream)
	assert.Equal(t, protocol.LinesItemStreamStderr, *items[1].Stream)
	assert.Equal(t, "boom", items[1].Text)

	require.NotNil(t, items[2].Stream)
	assert.Equal(t, protocol.LinesItemStreamStdout, *items[2].Stream)
	assert.Equal(t, "third", items[2].Text)

	assert.True(t, final, "fully-consumed terminal run is final")
}

func TestReadExecutionLogReplay_PaginationCursor(t *testing.T) {
	reason := model.ReasonSuccess
	run := &model.Run{
		ID:        testRunID,
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: &reason,
		CreatedAt: time.Now(),
	}
	dir := t.TempDir()
	writeRunLogRecords(t, dir, run, []logutil.LogLineRecord{
		{Stream: logutil.StreamStdout, Text: "a"},
		{Stream: logutil.StreamStdout, Text: "b"},
		{Stream: logutil.StreamStdout, Text: "c"},
		{Stream: logutil.StreamStdout, Text: "d"},
	})

	// First page: 2 of 4.
	page1, final1, err := readExecutionLogReplay(run, dir, 0, 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "a", page1[0].Text)
	assert.Equal(t, "b", page1[1].Text)
	assert.False(t, final1, "page that doesn't reach end-of-file must not be final")

	// Second page picks up from line 2.
	page2, final2, err := readExecutionLogReplay(run, dir, 2, 2)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.Equal(t, int64(2), page2[0].N)
	assert.Equal(t, "c", page2[0].Text)
	assert.Equal(t, "d", page2[1].Text)
	assert.True(t, final2, "page that consumes remaining lines on terminal run is final")
}

func TestReadExecutionLogReplay_LimitClampedToMax(t *testing.T) {
	reason := model.ReasonSuccess
	run := &model.Run{
		ID:        testRunID,
		TaskName:  "t1",
		Status:    model.PhaseEnded,
		EndReason: &reason,
		CreatedAt: time.Now(),
	}
	dir := t.TempDir()

	entries := make([]logutil.LogLineRecord, maxProtocolLogLines+10)
	for i := range entries {
		entries[i] = logutil.LogLineRecord{Stream: logutil.StreamStdout, Text: "x"}
	}
	writeRunLogRecords(t, dir, run, entries)

	items, final, err := readExecutionLogReplay(run, dir, 0, int64(len(entries))+1)
	require.NoError(t, err)
	assert.Len(t, items, maxProtocolLogLines, "limit must be clamped at maxProtocolLogLines")
	assert.False(t, final, "page that doesn't reach total must not be final")
}

func TestHandleLogReplayRequest_WithValidRun(t *testing.T) {
	repo := &stubRunRepo{run: &model.Run{
		ID:        testRunID,
		TaskName:  "task1",
		Status:    model.PhaseEnded,
		CreatedAt: time.Now(),
	}}
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	chunk, err := h.HandleLogReplayRequest(context.Background(), protocol.LogReplayRequestMessage{
		RequestID:   "req-1",
		ExecutionID: "exec-1",
	})
	require.NoError(t, err)
	assert.True(t, chunk.Final)
}
