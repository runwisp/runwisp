// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

	s, err := New(Options{
		DB:          repo,
		TaskManager: jm,
		Tasks:       tasks,
		Scheduler:   scheduler,
		Port:        9477,
		LogDir:      tmpDir,
		EventBus:    eb,
		Password:    "secret",
		JWTSecret:   "test-jwt-secret",
	})
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

	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	req := httptest.NewRequest("POST", "/api/tasks/task1/run", nil)
	w := httptest.NewRecorder()

	addAuth(req, s)
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var run model.Run
	err := json.Unmarshal(w.Body.Bytes(), &run)
	require.NoError(t, err)
	assert.Equal(t, "task1", run.TaskName)

	// Wait for async execution
	time.Sleep(50 * time.Millisecond)
	exec.AssertExpectations(t)
}

func TestGetAllRuns(t *testing.T) {
	s, repo, _, _ := setupServer(t)

	runs := []model.Run{
		{ID: ulid.Make().String(), TaskName: "task1"},
	}
	repo.On("QueryRuns", mock.Anything, "", 50, 0, "", storage.SortColumnDefault, storage.SortDirectionDefault, "").Return(runs, nil)
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
	repo.On("QueryRuns", mock.Anything, "task1", 50, 0, "", storage.SortColumnDefault, storage.SortDirectionDefault, "").Return(runs, nil)
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

func TestAuth(t *testing.T) {
	// Setup server with auth enabled
	repo := new(testutil.MockRunRepository)
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := runtime.NewTaskManager(exec, eb, time.Now)
	tasks := map[string]*model.Task{}

	// Create scheduler (nil is fine for this test)
	scheduler := runtime.NewScheduler(jm, tasks, time.UTC)

	s, err := New(Options{
		DB:          repo,
		TaskManager: jm,
		Tasks:       tasks,
		Scheduler:   scheduler,
		Port:        9477,
		LogDir:      "/tmp/logs",
		EventBus:    eb,
		Password:    "secret",
		JWTSecret:   "test-jwt-secret",
	})
	require.NoError(t, err)

	// Test Auth Status
	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"auth_required":true`)

	// Test Login Success
	// Step 1: Get challenge nonce
	req = httptest.NewRequest("GET", "/api/auth/challenge", nil)
	w = httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var challengeResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &challengeResp)
	nonce := challengeResp["nonce"]
	assert.NotEmpty(t, nonce)

	// Step 2: Compute SHA-256(password:nonce) and send
	h := sha256.Sum256([]byte("secret:" + nonce))
	challengeResponse := hex.EncodeToString(h[:])
	loginBody := fmt.Sprintf(`{"nonce":%q,"response":%q}`, nonce, challengeResponse)
	req = httptest.NewRequest("POST", "/api/auth", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	token := resp["token"]
	assert.NotEmpty(t, token)

	// Test Login Failure — wrong password
	req = httptest.NewRequest("GET", "/api/auth/challenge", nil)
	w = httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	json.Unmarshal(w.Body.Bytes(), &challengeResp)
	nonce = challengeResp["nonce"]
	wrongH := sha256.Sum256([]byte("wrong:" + nonce))
	wrongResponse := hex.EncodeToString(wrongH[:])
	loginBody = fmt.Sprintf(`{"nonce":%q,"response":%q}`, nonce, wrongResponse)
	req = httptest.NewRequest("POST", "/api/auth", strings.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test Protected Route without Token
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	w = httptest.NewRecorder()

	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Test Protected Route with Token
	req = httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()

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
	require.NoError(t, os.WriteFile(logPath+".idx", nil, 0644))

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
	require.NoError(t, os.WriteFile(logPath+".idx", nil, 0644))

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
	s, _, _, _ := setupServer(t)

	svcName := "svc1"
	svcTask := &model.Task{
		Name: svcName,
		Run:  "tail -f /dev/null",
		Kind: model.KindService,
	}
	s.tasks[svcName] = svcTask
	s.runService.tasks[svcName] = svcTask
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
