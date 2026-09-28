// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
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

func TestTokenMatches(t *testing.T) {
	tokens := []string{hookTokenA, hookTokenB}
	assert.True(t, tokenMatches(hookTokenA, tokens))
	assert.True(t, tokenMatches(hookTokenB, tokens))
	assert.False(t, tokenMatches(hookTokenA+"x", tokens), "wrong token")
	assert.False(t, tokenMatches(hookTokenA[:10], tokens), "prefix of a token")
	assert.False(t, tokenMatches("", tokens), "empty token")
	assert.False(t, tokenMatches(hookTokenA, nil), "no tokens configured")
}

// setupHookServer is setupServer with trigger tokens on task1.
func setupHookServer(t *testing.T, mutate func(*Options)) (*Server, *testutil.MockExecutor) {
	s, _, exec, _ := setupServerWithOpts(t, mutate)
	task, ok := s.runService.tasks.Get("task1")
	require.True(t, ok)
	task.TriggerTokens = []string{hookTokenA, hookTokenB}
	s.runService.tasks.Set(task)
	return s, exec
}

func postHook(s *Server, taskName, authorization string) *httptest.ResponseRecorder {
	return postHookURL(s, "/api/hooks/"+taskName, authorization)
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
func TestHookTrigger_RejectionsAreIndistinguishable(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	tokenless := &model.Task{Name: "tokenless", Run: "true", ManualTrigger: true}
	s.runService.tasks.Set(tokenless)

	cases := map[string]*httptest.ResponseRecorder{
		"missing header": postHook(s, "task1", ""),
		"wrong token":    postHook(s, "task1", "Bearer "+hookTokenA+"x"),
		"unknown task":   postHook(s, "does-not-exist", "Bearer "+hookTokenA),
		"tokenless task": postHook(s, "tokenless", "Bearer "+hookTokenA),
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
func TestHookTrigger_TokenRequiredWithAuthOff(t *testing.T) {
	s, _ := setupHookServer(t, func(o *Options) { o.NoAuth = true })
	assert.Equal(t, http.StatusUnauthorized, postHook(s, "task1", "").Code)
}

func TestHookTrigger_ValidTokenTriggersAsToken(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})
	done := make(chan struct{}, 1)
	unsub := s.eventBus.Subscribe(events.EventRunCompleted, func(events.Event) {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	defer unsub()

	w := postHook(s, "task1", "Bearer "+hookTokenB)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var run model.Run
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &run))
	assert.Equal(t, "task1", run.TaskName)
	assert.Equal(t, model.TriggeredByToken, run.TriggeredBy)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not complete")
	}
}

func TestHookTrigger_QueryToken(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})

	assert.Equal(t, http.StatusCreated, postHookURL(s, "/api/hooks/task1?token="+hookTokenA, "").Code)
	assert.Equal(t, http.StatusUnauthorized, postHookURL(s, "/api/hooks/task1?token=nope", "").Code)
}

// Only rejected tokens count toward the lockout: a valid caller can fire far
// past hookMaxFailures, while a guesser is cut off (even if it then presents a
// valid token) and other IPs are unaffected.
func TestHookTrigger_FailureLimiter(t *testing.T) {
	s, exec := setupHookServer(t, nil)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0})
	task, _ := s.runService.tasks.Get("task1")
	task.OnOverlap = model.PolicySkip // keep valid triggers from piling into a queue
	s.runService.tasks.Set(task)

	post := func(ip, auth string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/hooks/task1", nil)
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

// The token authorizes a trigger only; manual_trigger = false still wins.
func TestHookTrigger_ManualTriggerDisabled(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	task, _ := s.runService.tasks.Get("task1")
	task.ManualTrigger = false
	s.runService.tasks.Set(task)

	assert.Equal(t, http.StatusForbidden, postHook(s, "task1", "Bearer "+hookTokenA).Code)
}

func TestGetTask_NeverExposesTriggerTokens(t *testing.T) {
	s, _ := setupHookServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/tasks/task1", nil)
	addAuth(req, s)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), hookTokenA)
	assert.NotContains(t, w.Body.String(), hookTokenB)
}
