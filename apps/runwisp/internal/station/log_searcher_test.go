// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchExecutionLog_NilRun(t *testing.T) {
	hits, _, exhausted, err := searchExecutionLog(context.Background(), nil, "/tmp", logSearchParams{query: "x", limit: 10})
	require.NoError(t, err)
	assert.True(t, exhausted, "no run = nothing on disk to scan")
	assert.Nil(t, hits)
}

func TestSearchExecutionLog_MatchesSubstring(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseEnded, CreatedAt: time.Now()}
	dir := t.TempDir()
	writeRunLogRecords(t, dir, run, []logutil.LogLineRecord{
		{Stream: logutil.StreamStdout, Text: "starting up"},
		{Stream: logutil.StreamStderr, Text: "ERROR: boom"},
		{Stream: logutil.StreamStdout, Text: "all good"},
		{Stream: logutil.StreamStderr, Text: "ERROR: again"},
	})

	// Case-insensitive substring match on "error".
	hits, _, exhausted, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "error", limit: 10})
	require.NoError(t, err)
	require.Len(t, hits, 2)
	assert.Equal(t, "ERROR: boom", hits[0].Text)
	assert.Equal(t, int64(1), hits[0].N)
	require.NotNil(t, hits[0].Stream)
	assert.Equal(t, protocol.HitsItemStreamStderr, *hits[0].Stream)
	assert.Equal(t, "ERROR: again", hits[1].Text)
	assert.True(t, exhausted, "whole log scanned")
}

func TestSearchExecutionLog_PaginatesViaNextLine(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseRunning, CreatedAt: time.Now()}
	dir := t.TempDir()
	writeRunLogRecords(t, dir, run, []logutil.LogLineRecord{
		{Stream: logutil.StreamStdout, Text: "hit a"}, // N=0
		{Stream: logutil.StreamStdout, Text: "hit b"}, // N=1
		{Stream: logutil.StreamStdout, Text: "hit c"}, // N=2
	})

	// Budget of 2 hits: not exhausted, nextLine = first line still to scan (2).
	page1, nextLine, exhausted, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "hit a", page1[0].Text)
	assert.Equal(t, "hit b", page1[1].Text)
	assert.False(t, exhausted)
	assert.Equal(t, int64(2), nextLine, "resume after the last emitted line (N=1)")

	// Resume from the cursor: only "hit c" remains, scan now exhausted.
	page2, _, exhausted2, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 2, fromLine: nextLine})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "hit c", page2[0].Text)
	assert.True(t, exhausted2)
}

func TestSearchExecutionLog_PagesPastAHitOnLineZero(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseRunning, CreatedAt: time.Now()}
	dir := t.TempDir()
	writeRunLogRecords(t, dir, run, []logutil.LogLineRecord{
		{Stream: logutil.StreamStdout, Text: "hit a"}, // N=0
		{Stream: logutil.StreamStdout, Text: "hit b"}, // N=1
	})

	page1, nextLine, _, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 1})
	require.NoError(t, err)
	require.Len(t, page1, 1)
	page2, _, _, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 1, fromLine: nextLine})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "hit b", page2[0].Text, "page 2 must not re-emit the line-0 hit")
}

func TestSearchExecutionLog_BadRegexValidationError(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseEnded, CreatedAt: time.Now()}
	_, _, _, err := searchExecutionLog(context.Background(), run, t.TempDir(), logSearchParams{query: "([", regex: true, limit: 10})
	require.Error(t, err)
	ce, ok := err.(*StationError)
	require.True(t, ok)
	assert.Equal(t, StationErrorKindValidation, ce.Kind)
}

func TestHandleLogSearchRequest_UnknownExecutionExhausted(t *testing.T) {
	repo := &stubRunRepo{} // nil run -> ErrNotFound
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	chunk, err := h.HandleLogSearchRequest(context.Background(), protocol.LogSearchRequestMessage{
		RequestID:   "req-1",
		ExecutionID: "exec-unknown",
		Query:       "anything",
	})
	require.NoError(t, err)
	assert.True(t, chunk.Exhausted, "unknown execution = nothing to scan, exhausted")
	assert.Empty(t, chunk.Hits)
}

func TestHandleLogSearchRequest_BadRegexReportsKindOnce(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseEnded, CreatedAt: time.Now()}
	h := newDispatchInboundHandler(nil, &stubRunRepo{run: run}, executor.Availability{})

	_, err := h.HandleLogSearchRequest(context.Background(), protocol.LogSearchRequestMessage{
		RequestID: "r", ExecutionID: "exec-1", Query: "([", Regex: true,
	})
	require.Error(t, err)
	assert.Equal(t, StationErrorKindValidation, classifyErrorKind(err))
	assert.Equal(t, 1, strings.Count(err.Error(), string(StationErrorKindValidation)), "kind prefix must not repeat: %q", err.Error())
}

func TestHandleLogSearchRequest_MissingExecID(t *testing.T) {
	h := newDispatchInboundHandler(nil, &stubRunRepo{}, executor.Availability{})
	_, err := h.HandleLogSearchRequest(context.Background(), protocol.LogSearchRequestMessage{RequestID: "r", Query: "q"})
	require.Error(t, err)
}

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
