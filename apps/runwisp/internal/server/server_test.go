// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func setupServer(t *testing.T) (*Server, *testutil.MockRunRepository, *testutil.MockExecutor, string) {
	return setupServerWithOpts(t, nil)
}

// setupServerWithOpts builds the standard test server, letting a test tweak
// the Options (e.g. NoAuth) before construction.
func setupServerWithOpts(t *testing.T, mutate func(*Options)) (*Server, *testutil.MockRunRepository, *testutil.MockExecutor, string) {
	repo := new(testutil.MockRunRepository)
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := runtime.NewTaskManager(exec, eb, time.Now)

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		APITrigger:    true,
		MaxConcurrent: 1,
		OnOverlap:     model.PolicyQueue,
	}
	jm.UpsertTask(task)
	tasks := map[string]*model.Task{"task1": task}

	// Create scheduler (nil is fine for most tests)
	scheduler := runtime.NewScheduler(jm, tasks, time.UTC)

	// Create temp directory for logs
	tmpDir, err := os.MkdirTemp("", "runwisp-test-logs-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	opts := Options{
		DB:          repo,
		TaskManager: jm,
		Tasks:       runtime.NewTaskRegistry(tasks),
		Scheduler:   scheduler,
		Port:        9477,
		LogDir:      tmpDir,
		EventBus:    eb,
		Password:    "secret",
		JWTSecret:   "test-jwt-secret",
	}
	if mutate != nil {
		mutate(&opts)
	}
	s, err := New(opts)
	require.NoError(t, err)
	return s, repo, exec, tmpDir
}

func TestGetTasks(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var tasks []model.TaskResponse
	err := json.Unmarshal(w.Body.Bytes(), &tasks)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task1", tasks[0].Name)
}

func TestTriggerRun(t *testing.T) {
	s, _, exec, _ := setupServer(t)

	// The executor signals when the async run reaches it, so the test blocks on
	// that signal rather than sleeping and hoping.
	executed := make(chan struct{}, 1)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) {
			select {
			case executed <- struct{}{}:
			default:
			}
		}).
		Return(&executor.ExecuteResult{ExitCode: 0})

	req := httptest.NewRequest("POST", "/api/tasks/task1/run", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var run model.Run
	err := json.Unmarshal(w.Body.Bytes(), &run)
	require.NoError(t, err)
	assert.Equal(t, "task1", run.TaskName)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		t.Fatal("triggered run never reached the executor")
	}
	exec.AssertExpectations(t)
}

func TestGetAllRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	runs := []model.Run{
		{ID: ulid.Make().String(), TaskName: "task1"},
	}
	repo.On("QueryRuns", mock.Anything, storage.RunQuery{Limit: 50}).Return(runs, nil)
	repo.On("CountRunsFiltered", mock.Anything, "", "", "").Return(int64(len(runs)), nil)

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp RunsResponseBody
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp.Runs, 1)
	assert.Equal(t, int64(1), resp.Total)
}

func TestGetTaskRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	runs := []model.Run{
		{ID: ulid.Make().String(), TaskName: "task1"},
	}
	repo.On("QueryRuns", mock.Anything, storage.RunQuery{
		Filter: model.RunFilter{TaskName: "task1"},
		Limit:  50,
	}).Return(runs, nil)
	repo.On("CountRunsFiltered", mock.Anything, "", "task1", "").Return(int64(len(runs)), nil)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp RunsResponseBody
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp.Runs, 1)
	assert.Equal(t, int64(1), resp.Total)
}

func TestGetRunSummary(t *testing.T) {
	s, repo, _, _ := setupServer(t)
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{Total: 10, Success: 8, Failed: 2}, nil)

	req := httptest.NewRequest("GET", "/api/runs/summary", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var summary model.RunSummary
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &summary))
	assert.Equal(t, int64(10), summary.Total)
	assert.Equal(t, int64(8), summary.Success)
	assert.Equal(t, int64(2), summary.Failed)
}

func TestGetRunSummary_RepoError(t *testing.T) {
	s, repo, _, _ := setupServer(t)
	repo.On("GetRunSummary", mock.Anything).Return((*model.RunSummary)(nil), errors.New("db down"))

	req := httptest.NewRequest("GET", "/api/runs/summary", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetRun(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	run := &model.Run{ID: id, TaskName: "task1"}
	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id, nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp model.Run
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, id, resp.ID)
}

func TestDeleteRun(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	run := &model.Run{ID: id, TaskName: "task1", Status: model.PhaseEnded}
	repo.On("GetRun", mock.Anything, id).Return(run, nil)
	repo.On("SoftDeleteRuns", mock.Anything, mock.MatchedBy(func(sel model.RunSelector) bool {
		return !sel.MatchAll && len(sel.IDs) == 1 && sel.IDs[0] == id
	}), mock.Anything).Return([]storage.RunRef{{ID: id, TaskName: "task1"}}, nil)

	req := httptest.NewRequest("DELETE", "/api/tasks/task1/runs/"+id, nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestBulkDeleteRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id1 := ulid.Make().String()
	id2 := ulid.Make().String()
	repo.On("SoftDeleteRuns", mock.Anything, mock.MatchedBy(func(sel model.RunSelector) bool {
		return !sel.MatchAll && len(sel.IDs) == 2
	}), mock.Anything).Return([]storage.RunRef{
		{ID: id1, TaskName: "task1"},
		{ID: id2, TaskName: "task1"},
	}, nil)

	body, err := json.Marshal(model.RunSelector{IDs: []string{id1, id2}})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/runs/bulk/delete", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp BulkAffectedBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Affected)
}

func TestBulkRestoreRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	repo.On("RestoreRuns", mock.Anything, mock.MatchedBy(func(sel model.RunSelector) bool {
		return !sel.MatchAll && len(sel.IDs) == 1 && sel.IDs[0] == id
	})).Return([]model.Run{{ID: id, TaskName: "task1", Status: model.PhaseEnded}}, nil)

	body, err := json.Marshal(model.RunSelector{IDs: []string{id}})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/runs/bulk/restore", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp BulkAffectedBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 1, resp.Affected)
}

func TestBulkDeleteRuns_InvalidSelector(t *testing.T) {
	s, _, _, _ := setupServer(t)

	// Empty selector violates Validate() (no IDs, no MatchAll).
	body, err := json.Marshal(model.RunSelector{})
	require.NoError(t, err)
	req := httptest.NewRequest("POST", "/api/runs/bulk/delete", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	// huma returns 422 (or 400) for body-validation failures. Either is a
	// rejection — we just shouldn't get a 2xx.
	assert.GreaterOrEqual(t, w.Code, 400)
	assert.Less(t, w.Code, 500)
}

func TestStopRun(t *testing.T) {
	s, repo, exec, _ := setupServer(t)

	// Trigger a run so it's active in TaskManager (TerminateRun requires this)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		ctx := args.Get(0).(context.Context)
		<-ctx.Done()
	}).Return(&executor.ExecuteResult{ExitCode: 0})

	reqTrigger := httptest.NewRequest("POST", "/api/tasks/task1/run", nil)
	wTrigger := httptest.NewRecorder()
	addAuth(reqTrigger, s)
	s.router.ServeHTTP(wTrigger, reqTrigger)

	var triggeredRun model.Run
	json.Unmarshal(wTrigger.Body.Bytes(), &triggeredRun)

	triggeredRun.Status = model.PhaseRunning
	repo.On("GetRun", mock.Anything, triggeredRun.ID).Return(&triggeredRun, nil)

	req := httptest.NewRequest("POST", "/api/tasks/task1/runs/"+triggeredRun.ID+"/stop", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetLogRaw(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	id := ulid.Make().String()
	now := time.Now()

	run := &model.Run{ID: id, TaskName: "task1", Status: model.PhaseEnded, CreatedAt: now}
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	require.NoError(t, os.WriteFile(logPath, []byte("log content\n"), 0644))

	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id+"/log/raw", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "log content\n", w.Body.String())
}

func TestGetRunNotFound(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	id := ulid.Make().String()
	repo.On("GetRun", mock.Anything, id).Return(nil, storage.ErrNotFound)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id, nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestInvalidRunID(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/invalid-id", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// chapChallenge fetches a fresh login nonce from the challenge endpoint.
func chapChallenge(t *testing.T, s *Server) string {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/auth/challenge", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["nonce"], "challenge must return a nonce")
	return resp["nonce"]
}

// chapAttempt performs a full CHAP handshake (challenge → SHA-256(password:nonce)
// → login) and returns the login response recorder for the caller to assert on.
func chapAttempt(t *testing.T, s *Server, password string) *httptest.ResponseRecorder {
	t.Helper()
	nonce := chapChallenge(t, s)
	h := sha256.Sum256([]byte(password + ":" + nonce))
	body := fmt.Sprintf(`{"nonce":%q,"response":%q}`, nonce, hex.EncodeToString(h[:]))
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func TestAuthStatus_ReportsAuthRequired(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"auth_required":true`)
}

func TestCHAPLogin_Success(t *testing.T) {
	s, _, _, _ := setupServer(t)

	w := chapAttempt(t, s, "secret")

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp["token"], "a correct response must mint a session token")
}

func TestCHAPLogin_WrongPassword(t *testing.T) {
	s, _, _, _ := setupServer(t)

	w := chapAttempt(t, s, "wrong")

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProtectedRoute_RejectedWithoutToken(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProtectedRoute_AcceptedWithValidToken(t *testing.T) {
	s, _, _, _ := setupServer(t)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(chapAttempt(t, s, "secret").Body.Bytes(), &resp))

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+resp["token"])
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRunsStream(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/runs/stream", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)

	// This sequences a concurrent publish against the blocking SSE handler: the
	// handler subscribes synchronously near the top of ServeHTTP, so the first
	// sleep is a generous margin for that (not an async-completion wait), then we
	// publish and give the for-loop a beat to write before cancelling. There is
	// no completion signal to await here without a flush-watching ResponseWriter,
	// which would be more brittle than this bounded coordination.
	go func() {
		time.Sleep(100 * time.Millisecond)
		s.eventBus.Publish(events.EventRunCreated, events.RunEvent{Run: &model.Run{ID: ulid.Make().String()}})
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "event: ping", "initial ping should flush headers immediately")
	assert.Contains(t, w.Body.String(), "event: run.created")
}

func TestLogStream(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	id := ulid.Make().String()
	now := time.Now()

	run := &model.Run{ID: id, TaskName: "task1", Status: model.PhaseRunning, CreatedAt: now}
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	require.NoError(t, os.WriteFile(logPath, []byte("line 1\n"), 0644))

	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id+"/log/stream", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req = req.WithContext(ctx)

	go func() {
		time.Sleep(100 * time.Millisecond)
		// The line-based streamer publishes events directly; the file write
		// is unnecessary for the wire shape but kept so backfill remains
		// representative.
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString("line 2\n")
			f.Close()
		}
		s.eventBus.Publish(events.EventLogLine, events.LogLineEvent{
			RunID:   id,
			LineNum: 1,
			Stream:  "stdout",
			Text:    "line 2",
		})
		time.Sleep(200 * time.Millisecond)
		s.eventBus.Publish(events.EventRunCompleted, events.RunEvent{Run: run})
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "line 1")
	assert.Contains(t, body, "line 2")
	assert.Contains(t, body, "event: line")
	assert.Contains(t, body, "event: done")
}

func TestGetLogPage_NegativeFrom_Tail(t *testing.T) {
	s, repo, _, logDir := setupServer(t)

	id := ulid.Make().String()
	now := time.Now()

	run := &model.Run{ID: id, TaskName: "task1", Status: model.PhaseEnded, CreatedAt: now}
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))

	var content strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	require.NoError(t, os.WriteFile(logPath, []byte(content.String()), 0644))

	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id+"/log?from=-5", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var page LogPageBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))

	assert.Equal(t, int64(20), page.TotalLines)
	assert.True(t, page.Finalized)
	assert.False(t, page.Truncated)
	require.Len(t, page.Lines, 5)
	assert.Equal(t, int64(15), page.Lines[0].N)
	assert.Equal(t, "line 16", page.Lines[0].Text)
	assert.Equal(t, int64(19), page.Lines[4].N)
	assert.Equal(t, "line 20", page.Lines[4].Text)
}

func TestGetLogPage_FromZero_FirstLine(t *testing.T) {
	// Regression: from=0 (the Web UI's scroll-to-top request) must return the
	// FIRST lines, not be conflated with an absent `from` that defaults to the
	// -1000 tail anchor — otherwise line 0 never arrives and the viewport loads
	// forever. The anchors only diverge when totalLines > limit, so seed >1000
	// lines: from=0 starts at line 0, absent from starts at line 100.
	s, repo, _, logDir := setupServer(t)

	id := ulid.Make().String()
	now := time.Now()
	run := &model.Run{ID: id, TaskName: "task1", Status: model.PhaseEnded, CreatedAt: now}
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))

	const total = 1100
	var content strings.Builder
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&content, "line %d\n", i)
	}
	require.NoError(t, os.WriteFile(logPath, []byte(content.String()), 0644))

	repo.On("GetRun", mock.Anything, id).Return(run, nil)

	// from=0 → the first lines.
	req := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id+"/log?from=0&limit=5", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var page LogPageBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	assert.Equal(t, int64(total), page.TotalLines)
	require.Len(t, page.Lines, 5)
	assert.Equal(t, int64(0), page.Lines[0].N, "from=0 must return line 0 first")
	assert.Equal(t, "line 1", page.Lines[0].Text)
	assert.Equal(t, int64(4), page.Lines[4].N)

	// Absent from → default -1000 tail anchor: startLine = 1100-1000 = 100.
	reqDef := httptest.NewRequest("GET", "/api/tasks/task1/runs/"+id+"/log?limit=5", nil)
	wDef := httptest.NewRecorder()
	addAuth(reqDef, s)
	s.router.ServeHTTP(wDef, reqDef)

	assert.Equal(t, http.StatusOK, wDef.Code)
	var pageDef LogPageBody
	require.NoError(t, json.Unmarshal(wDef.Body.Bytes(), &pageDef))
	require.Len(t, pageDef.Lines, 5)
	assert.Equal(t, int64(100), pageDef.Lines[0].N, "absent from must use the -1000 tail anchor")
}

func addAuth(req *http.Request, s *Server) {
	_, ts, _ := s.auth.JWTAuth().Encode(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": auth.JWTIssuer,
		"aud": auth.JWTAudience,
	})
	req.Header.Set("Authorization", "Bearer "+ts)
}

// setupServerWithService returns a server containing a service task alongside
// the default "task1" cron task.
func setupServerWithService(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, exec, _ := setupServer(t)

	// A service instance is a long-running process: block the spawned run
	// until its context is cancelled (operator stop / restart / shutdown),
	// mirroring `tail -f /dev/null`. Without this the instant exit would
	// drive the supervisor's restart loop and Execute would be unmocked.
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0}, time.Hour)

	svcName := "svc1"
	svcTask := &model.Task{
		Name: svcName,
		Run:  "tail -f /dev/null",
		Kind: model.KindService,
	}
	s.runService.tasks.Set(svcTask)
	s.taskManager.UpsertTask(svcTask)
	return s, svcName
}

// ---- humaRestartService ----

func TestRestartServiceHTTP_Success(t *testing.T) {
	s, svcName := setupServerWithService(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+svcName+"/restart", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "Service instances restarting", body.Message)
}

func TestRestartServiceHTTP_TaskNotFound(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/nonexistent/restart", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRestartServiceHTTP_NotAService(t *testing.T) {
	s, _, _, _ := setupServer(t)

	// task1 is a cron task, not a service.
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/task1/restart", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ---- humaStopService ----

func TestStopServiceHTTP_Success(t *testing.T) {
	s, svcName := setupServerWithService(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+svcName+"/stop", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body struct {
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "Service stopped", body.Message)
}

func TestStopServiceHTTP_TaskNotFound(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/nonexistent/stop", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestStopServiceHTTP_NotAService(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/tasks/task1/stop", nil)
	w := httptest.NewRecorder()
	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRemoveStaleSocket_MissingPathNoError(t *testing.T) {
	if err := removeStaleSocket(filepath.Join(t.TempDir(), "nope.sock")); err != nil {
		t.Fatalf("removeStaleSocket on missing path: %v", err)
	}
}

func TestRemoveStaleSocket_RegularFileRefuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "imposter.sock")
	if err := os.WriteFile(p, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	err := removeStaleSocket(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a socket")
	// File must still exist.
	if _, statErr := os.Stat(p); statErr != nil {
		t.Fatalf("removeStaleSocket should not delete non-sockets: %v", statErr)
	}
}

func TestRemoveStaleSocket_SymlinkRefuses(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real")
	if err := os.WriteFile(target, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.sock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := removeStaleSocket(link)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

func TestRemoveStaleSocket_DeadSocketRemoved(t *testing.T) {
	dir := testutil.ShortTempDir(t)
	p := filepath.Join(dir, "dead.sock")
	// UnixListener auto-unlinks on Close; disable that so the socket file
	// remains on disk with no peer answering, mimicking a crashed daemon.
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("expected dead socket file to remain after Close: %v", err)
	}
	if err := removeStaleSocket(p); err != nil {
		t.Fatalf("removeStaleSocket on dead socket: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("expected dead socket file removed, got %v", err)
	}
}

func TestIsUnsupportedChmod(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"EINVAL tolerated", syscall.EINVAL, true},
		{"ENOTSUP tolerated", syscall.ENOTSUP, true},
		{"EOPNOTSUPP tolerated", syscall.EOPNOTSUPP, true},
		{"EPERM fatal", syscall.EPERM, false},
		{"wrapped EINVAL tolerated", fmt.Errorf("chmod: %w", syscall.EINVAL), true},
		{"nil-ish other fatal", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isUnsupportedChmod(tt.err))
		})
	}
}

func TestOpenUnixListener_EmptyPathRejected(t *testing.T) {
	srv := &Server{}
	_, err := srv.openUnixListener()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "socket path required")
}

func TestOpenUnixListener_HappyPathBindsAt0600(t *testing.T) {
	dir := testutil.ShortTempDir(t)
	p := filepath.Join(dir, "ok.sock")
	srv := &Server{socketPath: p}
	ln, err := srv.openUnixListener()
	require.NoError(t, err)
	defer ln.Close()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected socket perm 0600, got %#o", perm)
	}
}

func TestOpenUnixListener_PropagatesStaleSocketRejection(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "imposter.sock")
	if err := os.WriteFile(p, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{socketPath: p}
	_, err := srv.openUnixListener()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove stale socket")
}

func TestRemoveStaleSocket_LivePeerReports(t *testing.T) {
	dir := testutil.ShortTempDir(t)
	p := filepath.Join(dir, "live.sock")
	ln, err := net.Listen("unix", p)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	// Accept loop runs in the background so Dial succeeds inside removeStaleSocket.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	err = removeStaleSocket(p)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already listening")
	assert.Contains(t, err.Error(), "already be running")
	// File must still exist — caller will see EADDRINUSE on listen.
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("removeStaleSocket should not delete a live socket: %v", err)
	}
}
