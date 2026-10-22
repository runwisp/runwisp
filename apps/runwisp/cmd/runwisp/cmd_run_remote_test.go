// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// remoteDaemonStub is a fake daemon that implements the minimal REST surface
// runExecViaRemote needs: health, CHAP auth, trigger, log stream, and run
// fetch. It mints a fresh token on each login and (optionally) rejects a
// pre-seeded stale token on the first trigger to exercise the re-auth path.
type remoteDaemonStub struct {
	exitCode      int
	endReason     model.EndReason
	staleToken    string // if set, a trigger carrying this token gets one 401
	staleRejected atomic.Bool
	authCount     atomic.Int32
	streamHits    atomic.Int32
}

func (s *remoteDaemonStub) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/api/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "test-nonce"})

		case r.URL.Path == "/api/auth/login":
			s.authCount.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"token": freshToken})

		case strings.HasSuffix(r.URL.Path, "/run") && r.Method == http.MethodPost:
			auth := r.Header.Get("Authorization")
			if s.staleToken != "" && auth == "Bearer "+s.staleToken && !s.staleRejected.Load() {
				s.staleRejected.Store(true)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			require.Equal(t, "Bearer "+freshToken, auth, "trigger must carry the fresh token")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(model.Run{ID: "run-123", TaskName: "backup", Status: model.PhasePending})

		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			s.streamHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "event: line")
			fmt.Fprintln(w, `data: {"n":0,"stream":"stdout","text":"hello from backup"}`)
			fmt.Fprintln(w, "")
			fmt.Fprintln(w, "event: done")
			fmt.Fprintln(w, `data: {"final_line":0,"status":"ended"}`)
			fmt.Fprintln(w, "")

		case strings.HasPrefix(r.URL.Path, "/api/runs/"):
			reason := s.endReason
			_ = json.NewEncoder(w).Encode(model.Run{
				ID:        "run-123",
				TaskName:  "backup",
				Status:    model.PhaseEnded,
				EndReason: &reason,
				ExitCode:  s.exitCode,
			})

		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func TestRunExecViaRemote_StreamsAndPropagatesExitCode(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 7, endReason: model.ReasonFailed}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	code, err := runExecViaRemote(t.Context(), "backup", srv.URL, "pw", false, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 7, code, "exit code must propagate from the failed run")
	assert.Equal(t, int32(1), stub.streamHits.Load(), "the log stream must be followed")
	assert.Equal(t, int32(1), stub.authCount.Load(), "exactly one CHAP handshake")

	// The minted token is cached for reuse.
	assert.Equal(t, freshToken, loadCachedToken(srv.URL))
}

func TestRunExecViaRemote_SuccessReturnsZero(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	code, err := runExecViaRemote(t.Context(), "backup", srv.URL, "pw", false, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
}

func TestRunExecViaRemote_ReauthOnExpiredToken(t *testing.T) {
	useTempCacheDir(t)
	// Well-formed and unexpired, but the daemon rejects it (e.g. its password
	// rotated since the token was cached).
	stale := testSessions.IssueToken(time.Hour)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess, staleToken: stale}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	// Seed a cached (stale) token; the first trigger 401s and we re-handshake.
	storeCachedToken(srv.URL, "", stale)

	code, err := runExecViaRemote(t.Context(), "backup", srv.URL, "pw", false, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.True(t, stub.staleRejected.Load(), "the stale token must have been rejected once")
	assert.Equal(t, int32(1), stub.authCount.Load(), "exactly one re-authentication")
}

func TestRunExecViaRemote_Detach(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	code, err := runExecViaRemote(t.Context(), "backup", srv.URL, "pw", true, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, int32(0), stub.streamHits.Load(), "--detach must not follow the log stream")
}

func TestRunExecViaRemote_MissingPassword(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	_, err := runExecViaRemote(t.Context(), "backup", srv.URL, "", false, false, nil)
	require.Error(t, err)
	ufe, ok := isUserFacing(err)
	require.True(t, ok, "missing password must be a user-facing error")
	assert.Contains(t, ufe.Error(), "password is required")
}

// TestControlTargets_StopViaRemote exercises stop --url end to end: CHAP
// login, listing tasks to resolve "web" to a service, and dispatching the
// stop over the network — the same client path run --url uses.
func TestControlTargets_StopViaRemote(t *testing.T) {
	useTempCacheDir(t)
	var stoppedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "test-nonce"})
		case r.URL.Path == "/api/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": freshToken})
		case r.URL.Path == "/api/tasks":
			require.Equal(t, "Bearer "+freshToken, r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{"items":[{"name":"web","kind":"service","manualTrigger":true}]}`))
		case r.URL.Path == "/api/tasks/web/stop":
			require.Equal(t, "Bearer "+freshToken, r.Header.Get("Authorization"))
			stoppedPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Logf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	err := controlTargets(cmd, Flags{}, remoteFlags{URL: srv.URL, Password: "pw"}, []string{"web"}, stopVerb, false)
	require.NoError(t, err)
	assert.Equal(t, "/api/tasks/web/stop", stoppedPath)
	assert.Contains(t, buf.String(), `Service "web" stopped.`)
}

// A deploy task running as another user can't open the daemon's socket, so
// --token reaches the daemon over HTTP through the hook routes: no login, no
// task listing, the token as the bearer.
func TestControlWithToken_PostsHookRoute(t *testing.T) {
	useTempCacheDir(t)
	const token = "hook-token-for-shop"
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		hits = append(hits, r.Method+" "+r.URL.Path)
		switch {
		case r.Header.Get("Authorization") != "Bearer "+token:
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/api/hooks/tasks/shop/restart":
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/hooks/tasks/locked/restart":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"detail":"This hook token is not allowed to restart \"locked\""}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)

	err := controlTargets(cmd, Flags{}, remoteFlags{URL: srv.URL, Token: token}, []string{"shop"}, restartVerb, false)
	require.NoError(t, err)
	assert.Equal(t, []string{"POST /api/hooks/tasks/shop/restart"}, hits)
	assert.Contains(t, buf.String(), `"shop" restarted.`)

	err = controlTargets(cmd, Flags{}, remoteFlags{URL: srv.URL, Token: token}, []string{"locked"}, restartVerb, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allow list")

	err = controlTargets(cmd, Flags{}, remoteFlags{URL: srv.URL, Token: "wrong"}, []string{"shop"}, restartVerb, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rejected the hook token")
}

// A hook token can't list tasks or read logs, so anything that needs either is
// refused before a request goes out.
func TestControlWithToken_RejectsGlob(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	withURL := remoteFlags{URL: "http://127.0.0.1:1", Token: "tok"}

	cases := map[string]struct {
		rf     remoteFlags
		args   []string
		attach bool
		want   string
	}{
		"glob":     {withURL, []string{"web*"}, false, "literal task or service names"},
		"run ID":   {withURL, []string{"01J8Z3K9QK6VN8XG2R5F7T1C4M"}, false, "not the run ID"},
		"attach":   {withURL, []string{"web"}, true, "--attach can't be used with --token"},
		"no --url": {remoteFlags{Token: "tok"}, []string{"web"}, false, "--token needs --url"},
	}
	for name, tc := range cases {
		err := controlTargets(cmd, Flags{}, tc.rf, tc.args, startVerb, tc.attach)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
}

// Inside a task, "run it again under sudo" is the wrong advice: the task can't
// sudo, but it can carry a hook token.
func TestControlClient_InsideTaskHintsHookToken(t *testing.T) {
	t.Setenv("RUNWISP_INSTANCE_INDEX", "0")
	_, err := controlClient(t.Context(), Flags{DataDir: t.TempDir()}, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--token")
	assert.NotContains(t, err.Error(), "sudo")
}

func TestRunExecViaRemote_Unreachable(t *testing.T) {
	useTempCacheDir(t)
	// A closed server's URL is unroutable, so HealthCheck fails to dial.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := runExecViaRemote(t.Context(), "backup", url, "pw", false, false, nil)
	require.Error(t, err)
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "not reachable")
}

// `run` exited 1 on a task's first failed attempt while the daemon went on to
// retry it, so a CI step failed even when the retry passed. It follows the
// retry and exits with the final outcome.
func TestRunExecViaRemote_FollowsRetryToFinalOutcome(t *testing.T) {
	useTempCacheDir(t)
	failed, success := model.ReasonFailed, model.ReasonSuccess
	first := model.Run{ID: "run-1", TaskName: "backup", Status: model.PhaseEnded, EndReason: &failed, ExitCode: 1, IsFailure: true}
	retryOf := first.ID
	second := model.Run{ID: "run-2", TaskName: "backup", Status: model.PhaseEnded, EndReason: &success, RetryAttempt: 1, RetryOfRunID: &retryOf}
	zero := time.Duration(0)

	var mu sync.Mutex
	var streamed []string
	mux := serveTasks(http.NewServeMux(), model.TaskResponse{Task: model.Task{Name: "backup", ManualTrigger: true, RetryAttempts: 2, RetryDelay: &zero}})
	mux.HandleFunc("POST /api/tasks/backup/run", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(model.Run{ID: first.ID, TaskName: "backup", Status: model.PhasePending})
	})
	mux.HandleFunc("GET /api/runs/{id}/log/stream", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		streamed = append(streamed, r.PathValue("id"))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: done\ndata: {\"final_line\":0,\"status\":\"ended\"}\n\n")
	})
	mux.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		run := first
		if r.PathValue("id") == second.ID {
			run = second
		}
		_ = json.NewEncoder(w).Encode(run)
	})
	mux.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(server.RunsResponseBody{Total: 2, Items: []model.Run{second, first}})
	})
	var logins atomic.Int32
	srv := httptest.NewServer(remoteAuth(mux, &logins))
	defer srv.Close()

	code, err := runExecViaRemote(t.Context(), "backup", srv.URL, "pw", false, false, nil)
	require.NoError(t, err)
	assert.Equal(t, 0, code, "the retry passed, so the run passed")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"run-1", "run-2"}, streamed, "both attempts' logs are followed")
}

func TestPendingRetry(t *testing.T) {
	failed, success, stopped := model.ReasonFailed, model.ReasonSuccess, model.ReasonStopped
	delay := 5 * time.Second
	task := &model.Task{Name: "backup", RetryAttempts: 2, RetryDelay: &delay}
	for _, tc := range []struct {
		name      string
		task      *model.Task
		run       model.Run
		want      bool
		wantDelay time.Duration
	}{
		{name: "first failed attempt retries", task: task, run: model.Run{EndReason: &failed, IsFailure: true}, want: true, wantDelay: 5 * time.Second},
		{name: "last attempt is final", task: task, run: model.Run{EndReason: &failed, IsFailure: true, RetryAttempt: 2}},
		{name: "success is final", task: task, run: model.Run{EndReason: &success}},
		{name: "failure the policy forgives is final", task: task, run: model.Run{EndReason: &failed}},
		{name: "a stop is never retried", task: task, run: model.Run{EndReason: &stopped, IsFailure: true}},
		{name: "unknown task follows one run", run: model.Run{EndReason: &failed, IsFailure: true}},
		{name: "no retries configured", task: &model.Task{Name: "backup"}, run: model.Run{EndReason: &failed, IsFailure: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pendingRetry(tc.task, &tc.run)
			assert.Equal(t, tc.want, ok)
			assert.Equal(t, tc.wantDelay, got)
		})
	}
}
