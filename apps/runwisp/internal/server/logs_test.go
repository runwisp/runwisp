// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseResumeID(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		want   int64
		wantOK bool
	}{
		{"empty", "", 0, false},
		{"whitespace", "   ", 0, false},
		{"valid positive", "42", 42, true},
		{"zero", "0", 0, true},
		{"negative", "-1", 0, false},
		{"non-numeric", "abc", 0, false},
		{"float", "1.5", 0, false},
		{"leading and trailing whitespace", "  10  ", 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, ok := parseResumeID(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, n)
			}
		})
	}
}

// logsTestServer wires a Server with just the fields the log handlers need:
// a real in-memory SQLite RunRepository and a temp logDir on disk.
func logsTestServer(t *testing.T) (*Server, storage.Database, string) {
	t.Helper()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	logDir := t.TempDir()
	srv := &Server{db: db, logDir: logDir}
	return srv, db, logDir
}

// seedTerminalRun inserts a ended-with-success run for taskName and returns it.
func seedTerminalRun(t *testing.T, db storage.RunRepository, taskName string) *model.Run {
	t.Helper()
	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(t.Context(), run))
	return run
}

func TestResolveLogPath_InvalidULIDReturns400(t *testing.T) {
	srv, _, _ := logsTestServer(t)
	_, _, err := srv.resolveLogPath(t.Context(), "not-a-ulid")
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, statusErr.GetStatus())
}

func TestResolveLogPath_MissingRunReturns404(t *testing.T) {
	srv, _, _ := logsTestServer(t)
	_, _, err := srv.resolveLogPath(t.Context(), ulid.Make().String())
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, statusErr.GetStatus())
}

// TestResolveLogPath_StorageErrorReturns500 is the regression test for a bug
// where any non-"not found" failure from the run lookup (a locked DB, a disk
// I/O error) was silently reported as a plain 404 "Run not found", identical
// to a bad run ID, with nothing logged anywhere. A genuine storage failure
// must surface as a 500 so the operator can see it.
func TestResolveLogPath_StorageErrorReturns500(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	require.NoError(t, db.Close()) // any query now fails with a non-ErrNoRows error

	_, _, err := srv.resolveLogPath(t.Context(), run.ID)
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusInternalServerError, statusErr.GetStatus(),
		"a storage failure must not masquerade as a 404")
}

func TestResolveLogPath_Success(t *testing.T) {
	srv, db, logDir := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	got, gotRun, err := srv.resolveLogPath(t.Context(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, gotRun.ID)
	assert.Equal(t, logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt), got)
}

// writeLogLines writes the given lines (plus newlines) to the resolved log
// path for run, returning the log path.
func writeLogLines(t *testing.T, srv *Server, run *model.Run, lines ...string) string {
	t.Helper()
	logPath := logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	f, err := os.Create(logPath)
	require.NoError(t, err)
	defer f.Close()
	for _, l := range lines {
		_, err = f.WriteString(l + "\n")
		require.NoError(t, err)
	}
	return logPath
}

func TestHumaGetLogPage_InvalidULIDReturns400(t *testing.T) {
	srv, _, _ := logsTestServer(t)
	_, err := srv.humaGetLogPage(context.Background(), &LogPageInput{
		RunID: "not-a-ulid",
	})
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, statusErr.GetStatus())
}

func TestHumaGetLogPage_RunNotFoundReturns404(t *testing.T) {
	srv, _, _ := logsTestServer(t)
	_, err := srv.humaGetLogPage(context.Background(), &LogPageInput{
		RunID: ulid.Make().String(),
	})
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, statusErr.GetStatus())
}

func TestHumaGetLogPage_DefaultsAndFinalizedFlag(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	writeLogLines(t, srv, run, "alpha", "beta", "gamma")

	out, err := srv.humaGetLogPage(context.Background(), &LogPageInput{
		RunID: run.ID,
	})
	require.NoError(t, err)
	assert.True(t, out.Body.Finalized, "ended run should be finalized")
	assert.NotEmpty(t, out.Body.Lines)
}

func TestHumaGetLogPage_RespectsClampedLimit(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	writeLogLines(t, srv, run, "a", "b", "c", "d")

	// Limit above max should be clamped silently.
	out, err := srv.humaGetLogPage(context.Background(), &LogPageInput{
		RunID: run.ID,
		From:  1,
		Limit: LogPageMaxLimit + 5_000,
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(out.Body.Lines), 4)
}

// invokeStreamResponse drives a *huma.StreamResponse's Body callback against a
// real httptest.ResponseRecorder wrapped in a minimal huma.Context (the same
// humachi adapter the live router uses), so tests can assert on headers/body
// without spinning up the full server + router + auth stack.
func invokeStreamResponse(t *testing.T, resp *huma.StreamResponse) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	resp.Body(humachi.NewContext(&huma.Operation{}, req, w))
	return w
}

func TestHumaGetLogRaw_PrependsPrevSegment(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	logPath := writeLogLines(t, srv, run, "current-line")
	require.NoError(t, os.WriteFile(logutil.PrevPath(logPath), []byte("rotated-line\n"), 0o600))

	out, err := srv.humaGetLogRaw(context.Background(), &LogRawInput{
		RunID: run.ID,
	})
	require.NoError(t, err)
	w := invokeStreamResponse(t, out)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "rotated-line\ncurrent-line\n", w.Body.String())
}

func TestHumaGetLogRaw_MissingBothFilesIsEmpty(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	out, err := srv.humaGetLogRaw(context.Background(), &LogRawInput{
		RunID: run.ID,
	})
	require.NoError(t, err)
	w := invokeStreamResponse(t, out)
	assert.Empty(t, w.Body.String())
}

// TestHumaGetLogRaw_StreamsFullContent guards against the raw log endpoint
// silently regressing to unbounded memory use: unlike the JSON page
// (LogPageMaxLimit), the SSE replay (LogStreamReplayMax), or search
// (LogSearchMaxLimit), /log/raw has no size cap and must return the entire
// file untruncated no matter how large it is. The handler now streams the
// file straight to the response writer via io.Copy instead of buffering it
// into a single []byte first (see streamRawLog), so this only asserts the
// externally observable behavior is unchanged: full, untruncated content.
func TestHumaGetLogRaw_StreamsFullContent(t *testing.T) {
	srv, db, _ := logsTestServer(t)
	run := seedTerminalRun(t, db, "task")
	logPath := logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))

	big := strings.Repeat("x", 5*1024*1024) // 5 MiB, far past any line-count cap in bytes.
	require.NoError(t, os.WriteFile(logPath, []byte(big), 0o600))

	out, err := srv.humaGetLogRaw(context.Background(), &LogRawInput{RunID: run.ID})
	require.NoError(t, err)
	w := invokeStreamResponse(t, out)
	assert.Equal(t, big, w.Body.String(), "raw log handler must return the full log with no size cap or truncation")
}

func TestHumaGetLogRaw_PropagatesResolveError(t *testing.T) {
	srv, _, _ := logsTestServer(t)
	_, err := srv.humaGetLogRaw(context.Background(), &LogRawInput{
		RunID: "not-a-ulid",
	})
	require.Error(t, err)
	statusErr, ok := err.(huma.StatusError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, statusErr.GetStatus())
}

func TestStreamRawLog_OnlyCurrent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	require.NoError(t, os.WriteFile(p, []byte("hello"), 0o600))
	var buf bytes.Buffer
	require.NoError(t, streamRawLog(&buf, p))
	assert.Equal(t, "hello", buf.String())
}

func TestStreamRawLog_OnlyPrev(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	require.NoError(t, os.WriteFile(logutil.PrevPath(p), []byte("rotated"), 0o600))
	var buf bytes.Buffer
	require.NoError(t, streamRawLog(&buf, p))
	assert.Equal(t, "rotated", buf.String())
}

func TestStreamRawLog_NeitherFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	require.NoError(t, streamRawLog(&buf, filepath.Join(dir, "missing.log")))
	assert.Empty(t, buf.String())
}

func TestStreamRawLog_PrevReadErrorIsPropagated(t *testing.T) {
	// Create a directory at the rotated-segment path so os.Open returns an
	// error that is not os.ErrNotExist.
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	require.NoError(t, os.Mkdir(logutil.PrevPath(p), 0o755))
	var buf bytes.Buffer
	require.Error(t, streamRawLog(&buf, p))
}

func TestStreamRawLog_CurrentReadErrorIsPropagated(t *testing.T) {
	// Directory at logPath itself causes a non-ENOENT read error.
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	require.NoError(t, os.Mkdir(p, 0o755))
	var buf bytes.Buffer
	require.Error(t, streamRawLog(&buf, p))
}
