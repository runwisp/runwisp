// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	hookTokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hookTokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestPresentedToken(t *testing.T) {
	cases := []struct {
		name, header, query, want string
	}{
		{"bearer header", "Bearer abc", "", "abc"},
		{"scheme is case-insensitive", "bearer abc", "", "abc"},
		{"query when no header", "", "abc", "abc"},
		{"header wins over query", "Bearer abc", "xyz", "abc"},
		{"malformed header does not fall back to query", "abc", "xyz", ""},
		{"wrong scheme", "Basic abc", "", ""},
		{"nothing sent", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, presentedToken(tc.header, tc.query))
		})
	}
}

func TestMatchHookToken(t *testing.T) {
	tokens := []model.HookToken{{Token: hookTokenA}, {Token: hookTokenB, Allow: []model.HookAction{model.HookStop}}}
	got, ok := matchHookToken(hookTokenB, tokens)
	require.True(t, ok)
	assert.Equal(t, tokens[1], got, "returns the matched entry with its allow list")
	_, ok = matchHookToken(hookTokenA, tokens)
	assert.True(t, ok)
	for name, presented := range map[string]string{"wrong token": hookTokenA + "x", "prefix of a token": hookTokenA[:10], "empty token": ""} {
		_, ok := matchHookToken(presented, tokens)
		assert.False(t, ok, name)
	}
	_, ok = matchHookToken(hookTokenA, nil)
	assert.False(t, ok, "no tokens configured")
}

// setupHookServer is setupServer with hook tokens on task1: tokenA may do
// anything, tokenB may only stop.
func setupHookServer(t *testing.T, mutate func(*Options)) (*Server, *testutil.MockExecutor) {
	s, _, exec, _ := setupServerWithOpts(t, mutate)
	task, ok := s.runService.tasks.Get("task1")
	require.True(t, ok)
	task.HookTokens = []model.HookToken{{Token: hookTokenA}, {Token: hookTokenB, Allow: []model.HookAction{model.HookStop}}}
	s.runService.tasks.Set(task)
	return s, exec
}

func postHook(s *Server, taskName, action, authorization string) *httptest.ResponseRecorder {
	return postHookURL(s, "/api/hooks/tasks/"+taskName+"/"+action, authorization)
}

func postHookURL(s *Server, url, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, url, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// An unknown task, a task without tokens, and a wrong/missing token must be
// indistinguishable, so the endpoint can't be used to enumerate task names.
func TestHook_RejectionsAreIndistinguishable(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	tokenless := &model.Task{Name: "tokenless", Run: "true", ManualTrigger: true}
	s.runService.tasks.Set(tokenless)

	cases := map[string]*httptest.ResponseRecorder{
		"missing header": postHook(s, "task1", "run", ""),
		"wrong token":    postHook(s, "task1", "run", "Bearer "+hookTokenA+"x"),
		"unknown task":   postHook(s, "does-not-exist", "run", "Bearer "+hookTokenA),
		"tokenless task": postHook(s, "tokenless", "run", "Bearer "+hookTokenA),
		"stop, no token": postHook(s, "task1", "stop", ""),
	}
	want := cases["missing header"].Body.String()
	for name, w := range cases {
		assert.Equal(t, http.StatusUnauthorized, w.Code, name)
		assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"), name)
		assert.Equal(t, want, w.Body.String(), name)
	}
}

// RUNWISP_AUTH=off opens the session-protected API, not the hooks: the token is
// still required.
func TestHook_TokenRequiredWithAuthOff(t *testing.T) {
	s, _ := setupHookServer(t, func(o *Options) { o.NoAuth = true })
	assert.Equal(t, http.StatusUnauthorized, postHook(s, "task1", "run", "").Code)
}

func TestHook_RunTriggersAsHook(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})

	w := postHookURL(s, "/api/hooks/tasks/task1/run?wait=true", "Bearer "+hookTokenA)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var run model.Run
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &run))
	assert.Equal(t, "task1", run.TaskName)
	assert.Equal(t, model.TriggeredByHook, run.TriggeredBy)
	assert.Equal(t, model.PhaseEnded, run.Status, "wait=true returns the finished run")
}

func TestHook_StartRestartStop(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})

	assert.Equal(t, http.StatusNoContent, postHook(s, "task1", "start", "Bearer "+hookTokenA).Code)
	assert.Equal(t, http.StatusNoContent, postHook(s, "task1", "restart", "Bearer "+hookTokenA).Code)
	assert.Equal(t, http.StatusNoContent, postHookURL(s, "/api/hooks/tasks/task1/stop?wait=true", "Bearer "+hookTokenA).Code)
}

func TestHook_Service(t *testing.T) {
	s, svcName := setupServerWithService(t)
	svc, _ := s.runService.tasks.Get(svcName)
	svc.HookTokens = []model.HookToken{{Token: hookTokenA}}
	s.runService.tasks.Set(svc)
	auth := "Bearer " + hookTokenA

	assert.Equal(t, http.StatusNoContent, postHook(s, svcName, "restart", auth).Code)
	assert.Equal(t, http.StatusNoContent, postHook(s, svcName, "stop", auth).Code)
	assert.Equal(t, http.StatusNoContent, postHook(s, svcName, "start", auth).Code)
	assert.Equal(t, http.StatusBadRequest, postHookURL(s, "/api/hooks/tasks/"+svcName+"/start?wait=true", auth).Code,
		"a service instance never finishes, so start cannot wait on it")
}

// A token limited by allow is refused other actions with 403, which is not a
// guess and so does not count toward the failure lockout.
func TestHook_AllowList(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	for range hookMaxFailures + 1 {
		require.Equal(t, http.StatusForbidden, postHook(s, "task1", "run", "Bearer "+hookTokenB).Code)
	}
	assert.Equal(t, http.StatusNoContent, postHook(s, "task1", "stop", "Bearer "+hookTokenB).Code)
}

func TestHook_QueryToken(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})

	assert.Equal(t, http.StatusCreated, postHookURL(s, "/api/hooks/tasks/task1/run?token="+hookTokenA, "").Code)
	assert.Equal(t, http.StatusUnauthorized, postHookURL(s, "/api/hooks/tasks/task1/run?token=nope", "").Code)
}

// Only rejected tokens count toward the lockout: a valid caller can fire far
// past hookMaxFailures, while a guesser is cut off (even if it then presents a
// valid token) and other IPs are unaffected.
func TestHook_FailureLimiter(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})
	task, _ := s.runService.tasks.Get("task1")
	task.OnOverlap = model.PolicySkip // keep valid triggers from piling into a queue
	s.runService.tasks.Set(task)

	post := func(ip, auth string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/hooks/tasks/task1/run", nil)
		req.RemoteAddr = ip + ":1234"
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	for range 5 * hookMaxFailures {
		assert.NotEqual(t, http.StatusTooManyRequests, post("192.0.2.1", "Bearer "+hookTokenA),
			"successful triggers must never count toward the lockout")
	}

	for range hookMaxFailures {
		require.Equal(t, http.StatusUnauthorized, post("192.0.2.2", "Bearer wrong"))
	}
	assert.Equal(t, http.StatusTooManyRequests, post("192.0.2.2", "Bearer wrong"))
	assert.Equal(t, http.StatusTooManyRequests, post("192.0.2.2", "Bearer "+hookTokenA),
		"a locked-out IP is refused even with a valid token")
	assert.NotEqual(t, http.StatusTooManyRequests, post("192.0.2.3", "Bearer "+hookTokenA),
		"the lockout is per IP")
}

// manual_trigger locks the dashboard and CLI only; a hook token is its own
// explicit grant.
func TestHook_IgnoresManualTrigger(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})
	task, _ := s.runService.tasks.Get("task1")
	task.ManualTrigger = false
	s.runService.tasks.Set(task)

	assert.Equal(t, http.StatusCreated, postHook(s, "task1", "run", "Bearer "+hookTokenA).Code)
	assert.Equal(t, http.StatusNoContent, postHook(s, "task1", "stop", "Bearer "+hookTokenA).Code)
}

func TestGetTask_NeverExposesHookTokens(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task1", nil)
	addAuth(req, s)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), hookTokenA)
	assert.NotContains(t, w.Body.String(), hookTokenB)
}
