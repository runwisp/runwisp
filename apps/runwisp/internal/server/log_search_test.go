// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// writeRunLog creates the on-disk log for run, with the supplied lines.
func writeRunLog(t *testing.T, logDir string, run *model.Run, lines ...string) {
	t.Helper()
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	require.NoError(t, os.WriteFile(logPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
}

func TestSearchLogs_TaskWide(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-time.Hour)}
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run1, "hello", "connection refused: redis")
	writeRunLog(t, logDir, &run2, "Connection Refused: db", "ok")

	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return([]model.Run{run2, run1}, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q="+url.QueryEscape("connection refused"), nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Hits, 2)
	// Newest run first.
	assert.Equal(t, run2.ID, body.Hits[0].RunID)
	assert.Equal(t, run1.ID, body.Hits[1].RunID)
	assert.True(t, body.Exhausted)
	assert.Empty(t, body.NextCursor)
	assert.Equal(t, 2, body.ScannedRuns)
}

func TestSearchLogs_CaseSensitiveMisses(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "Hello", "WORLD")

	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return([]model.Run{run}, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=hello&case=true", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Hits, "lowercase q should not match Hello when case=true")
}

func TestSearchLogs_RegexValidation(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=%28unclosed&regex=true", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchLogs_SingleRun(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "alpha", "beta", "gamma")

	repo.On("GetRun", mock.Anything, run.ID).Return(&run, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=beta&runId="+run.ID, nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Hits, 1)
	assert.Equal(t, run.ID, body.Hits[0].RunID)
	assert.Equal(t, "beta", body.Hits[0].Text)
}

func TestSearchLogs_SingleRun_WrongTask(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	run := &model.Run{ID: id, TaskName: "other", CreatedAt: time.Now()}
	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/tasks/task1/log/search?q=foo&runId=%s", id), nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSearchLogs_BadCursor(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=foo&cursor=%21not-base64", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchLogs_NoRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return([]model.Run{}, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=anything", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Hits)
	assert.True(t, body.Exhausted)
}

// TestSearchLogs_AdvancesPastFirstPage guards the regression where search
// never scanned beyond the newest LogSearchRunPageSize runs: a match in an
// older run was unreachable and the terminal page lied about being exhausted.
// The first (full) page must yield a cursor; resuming it must advance the run
// window (Offset) and surface the older match.
func TestSearchLogs_AdvancesPastFirstPage(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	// A full first page of non-matching runs.
	firstPage := make([]model.Run, LogSearchRunPageSize)
	for i := range firstPage {
		run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-time.Duration(i) * time.Minute)}
		writeRunLog(t, logDir, &run, "nothing to see here")
		firstPage[i] = run
	}
	// An older run (second page) that holds the match.
	older := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-999 * time.Hour)}
	writeRunLog(t, logDir, &older, "the needle is here")

	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		Offset:        0,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return(firstPage, nil)
	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		Offset:        LogSearchRunPageSize,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return([]model.Run{older}, nil)

	// Page 1: no hits, but not exhausted — a cursor must advance the window.
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task1/log/search?q=needle", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Hits)
	assert.False(t, body.Exhausted, "a full page must not report exhausted")
	require.NotEmpty(t, body.NextCursor, "expected a window-advance cursor")

	// Page 2: resume advances to the older run and finds the match.
	req2 := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=needle&cursor="+url.QueryEscape(body.NextCursor), nil)
	w2 := httptest.NewRecorder()
	addAuth(req2, s)
	s.router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var body2 LogSearchBody
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &body2))
	require.Len(t, body2.Hits, 1)
	assert.Equal(t, older.ID, body2.Hits[0].RunID)
	assert.True(t, body2.Exhausted, "under-full second page is exhausted")
}

func TestSearchLogs_CursorResume(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "foo", "foo", "foo", "foo")

	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter:        model.RunFilter{TaskName: "task1"},
		Limit:         LogSearchRunPageSize,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortDesc,
	}).Return([]model.Run{run}, nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=foo&limit=2", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Hits, 2)
	require.NotEmpty(t, body.NextCursor, "expected a cursor with more lines available")

	req2 := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=foo&limit=2&cursor="+url.QueryEscape(body.NextCursor), nil)
	w2 := httptest.NewRecorder()
	addAuth(req2, s)
	s.router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var body2 LogSearchBody
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &body2))
	assert.Len(t, body2.Hits, 2)
}
