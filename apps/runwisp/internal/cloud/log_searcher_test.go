// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
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

	// Budget of 2 hits: not exhausted, nextLine = last emitted line (1). The
	// cursor's "skip lines <= fromLine" matches ScanRun.startAfterN exactly.
	// (A boundary exactly at line 0 cannot be expressed by that guard — the
	// cloud aggregator dedupes by (executionId, n) to absorb that rare case.)
	page1, nextLine, exhausted, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 2})
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.Equal(t, "hit a", page1[0].Text)
	assert.Equal(t, "hit b", page1[1].Text)
	assert.False(t, exhausted)
	assert.Equal(t, int64(1), nextLine, "resume after the last emitted line (N=1)")

	// Resume from the cursor: only "hit c" remains, scan now exhausted.
	page2, _, exhausted2, err := searchExecutionLog(context.Background(), run, dir, logSearchParams{query: "hit", limit: 2, fromLine: nextLine})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "hit c", page2[0].Text)
	assert.True(t, exhausted2)
}

func TestSearchExecutionLog_BadRegexValidationError(t *testing.T) {
	run := &model.Run{ID: testRunID, TaskName: "t1", Status: model.PhaseEnded, CreatedAt: time.Now()}
	_, _, _, err := searchExecutionLog(context.Background(), run, t.TempDir(), logSearchParams{query: "([", regex: true, limit: 10})
	require.Error(t, err)
	ce, ok := err.(*CloudError)
	require.True(t, ok)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleLogSearchRequest_UnknownExecutionExhausted(t *testing.T) {
	repo := &stubRunRepo{} // nil run -> ErrNotFound
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	chunk, err := h.HandleLogSearchRequest(context.Background(), protocol.LogSearchRequestMessage{
		ID:          "req-1",
		ExecutionID: "exec-unknown",
		Query:       "anything",
	})
	require.NoError(t, err)
	assert.True(t, chunk.Exhausted, "unknown execution = nothing to scan, exhausted")
	assert.Empty(t, chunk.Hits)
}

func TestHandleLogSearchRequest_MissingExecID(t *testing.T) {
	h := newDispatchInboundHandler(nil, &stubRunRepo{}, executor.Availability{})
	_, err := h.HandleLogSearchRequest(context.Background(), protocol.LogSearchRequestMessage{ID: "r", Query: "q"})
	require.Error(t, err)
}
