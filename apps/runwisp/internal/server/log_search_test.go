// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"errors"
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
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
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

func searchRows(runs ...model.Run) []storage.LogSearchRun {
	rows := make([]storage.LogSearchRun, 0, len(runs))
	for _, run := range runs {
		rows = append(rows, storage.LogSearchRun{
			RunRef:      storage.RunRef{ID: run.ID, TaskName: run.TaskName, CreatedAt: run.CreatedAt},
			CreationKey: run.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	return rows
}

func TestSearchLogs_TaskWide(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-time.Hour)}
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run1, "hello", "connection refused: redis")
	writeRunLog(t, logDir, &run2, "Connection Refused: db", "ok")

	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{}).Return(searchRows(run2, run1), nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q="+url.QueryEscape("connection refused"), nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	// Newest run first.
	assert.Equal(t, run2.ID, body.Items[0].RunID)
	assert.Equal(t, run1.ID, body.Items[1].RunID)
	assert.True(t, body.Exhausted)
	assert.Empty(t, body.NextCursor)
	assert.Equal(t, 2, body.ScannedRuns)
}

func TestSearchLogs_CaseSensitiveMisses(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "Hello", "WORLD")

	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{}).Return(searchRows(run), nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=hello&case=true", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Items, "lowercase q should not match Hello when case=true")
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
	require.Len(t, body.Items, 1)
	assert.Equal(t, run.ID, body.Items[0].RunID)
	assert.Equal(t, "beta", body.Items[0].Text)
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

// TestSearchLogs_RunLookupStorageErrorReturns500 is the regression test for a
// bug where any non-"not found" failure looking up runId (a locked DB, a disk
// I/O error) was silently reported as a plain 404, identical to a bad or
// unknown run ID, with nothing logged. A genuine storage failure must surface
// as a 500.
func TestSearchLogs_RunLookupStorageErrorReturns500(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	repo.On("GetRun", mock.Anything, id).Return((*model.Run)(nil), errors.New("disk read error"))

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/tasks/task1/log/search?q=foo&runId=%s", id), nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a storage failure must not masquerade as a 404")
}

// TestSearchLogs_QueryRunsErrorReturns500 is the sibling regression for the
// task-wide listing path: a QueryRuns failure must surface as a logged 500,
// not the same "Failed to list runs" 500 masking whatever actually broke —
// mapDomainError is what supplies the logging.
func TestSearchLogs_QueryRunsErrorReturns500(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{}).Return([]storage.LogSearchRun(nil), errors.New("disk read error"))

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q="+url.QueryEscape("connection refused"), nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
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

	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{}).Return(searchRows(), nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=anything", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Empty(t, body.Items)
	assert.True(t, body.Exhausted)
}

func TestSearchLogs_KeysetSurvivesHistoryChanges(t *testing.T) {
	for _, change := range []string{"delete boundary", "insert newer"} {
		t.Run(change, func(t *testing.T) {
			db, err := storage.New(":memory:")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })
			s, _, _, logDir := setupServerWithOpts(t, func(opts *Options) { opts.DB = db })

			// Use one creation timestamp to exercise the ID tie-breaker.
			now := time.Now().UTC()
			addRun := func(createdAt time.Time) string {
				run := model.Run{
					ID: ulid.Make().String(), TaskName: "task1", CreatedAt: createdAt,
					Status: model.PhaseEnded, TriggeredBy: model.TriggeredByAPI,
				}
				require.NoError(t, db.CreateRun(t.Context(), &run))
				writeRunLog(t, logDir, &run, "needle")
				return run.ID
			}
			oldest := addRun(now)
			for range LogSearchRunPageSize {
				addRun(now)
			}

			search := func(cursor string) LogSearchBody {
				req := httptest.NewRequest(http.MethodGet,
					"/api/tasks/task1/log/search?q=needle&cursor="+url.QueryEscape(cursor), nil)
				w := httptest.NewRecorder()
				addAuth(req, s)
				s.router.ServeHTTP(w, req)
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var body LogSearchBody
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				return body
			}

			first := search("")
			require.Len(t, first.Items, LogSearchRunPageSize)
			require.NotEmpty(t, first.NextCursor)
			if change == "delete boundary" {
				require.NoError(t, db.DeleteRunsByIDs(t.Context(), []string{first.Items[len(first.Items)-1].RunID}))
			} else {
				addRun(now.Add(time.Hour))
			}
			second := search(first.NextCursor)
			require.Len(t, second.Items, 1)
			assert.Equal(t, oldest, second.Items[0].RunID)
			assert.True(t, second.Exhausted)
		})
	}
}

func TestSearchLogs_CursorResume(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	now := time.Now()
	run := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now}
	writeRunLog(t, logDir, &run, "foo", "foo", "foo", "foo")

	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{}).Return(searchRows(run), nil)
	repo.On("ListLogSearchRuns", mock.Anything, "task1", LogSearchRunPageSize, storage.LogSearchBoundary{
		CreationKey: now.Format(time.RFC3339Nano), ID: run.ID, Include: true,
	}).Return(searchRows(run), nil)

	req := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=foo&limit=2", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var body LogSearchBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Items, 2)
	require.NotEmpty(t, body.NextCursor, "expected a cursor with more lines available")

	req2 := httptest.NewRequest(http.MethodGet,
		"/api/tasks/task1/log/search?q=foo&limit=2&cursor="+url.QueryEscape(body.NextCursor), nil)
	w2 := httptest.NewRecorder()
	addAuth(req2, s)
	s.router.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)

	var body2 LogSearchBody
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &body2))
	require.Len(t, body2.Items, 2)
	assert.EqualValues(t, 2, body2.Items[0].N)
	assert.EqualValues(t, 3, body2.Items[1].N)
}
