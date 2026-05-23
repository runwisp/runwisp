// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

	repo.On("QueryRuns", mock.Anything, "task1", LogSearchRunPageSize, 0, "",
		storage.SortColumnCreatedAt, storage.SortDesc, "").Return([]model.Run{run2, run1}, nil)

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

	repo.On("QueryRuns", mock.Anything, "task1", LogSearchRunPageSize, 0, "",
		storage.SortColumnCreatedAt, storage.SortDesc, "").Return([]model.Run{run}, nil)

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
		"/api/tasks/task1/log/search?q=beta&run_id="+run.ID, nil)
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
		fmt.Sprintf("/api/tasks/task1/log/search?q=foo&run_id=%s", id), nil)
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

	repo.On("QueryRuns", mock.Anything, "task1", LogSearchRunPageSize, 0, "",
		storage.SortColumnCreatedAt, storage.SortDesc, "").Return([]model.Run{}, nil)

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

func TestSearchLogs_CursorResume(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "foo", "foo", "foo", "foo")

	repo.On("QueryRuns", mock.Anything, "task1", LogSearchRunPageSize, 0, "",
		storage.SortColumnCreatedAt, storage.SortDesc, "").Return([]model.Run{run}, nil)

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

	// Resume with the cursor.
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
