// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestAuthOrLocalTrusted_BypassWithLocalContext verifies the middleware's
// fast path: a request flagged with localTrustedKey skips JWT verification
// entirely. This is the contract that lets the local CLI/TUI talk to the
// daemon without ever holding a password.
func TestAuthOrLocalTrusted_BypassWithLocalContext(t *testing.T) {
	authSvc, err := auth.NewService("pw", "jwt-secret-for-bypass-test", nil)
	require.NoError(t, err)

	called := false
	handler := authOrLocalTrusted(authSvc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/api/tasks", nil)
	r = r.WithContext(context.WithValue(r.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.True(t, called, "handler must run when local-trusted")
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestAuthOrLocalTrusted_RejectsTCPWithoutJWT verifies the slow path: a
// request without the local flag and without a JWT is unauthorized. Without
// this, removing the password from disk would silently expose the API to
// any local user that could reach the TCP port.
func TestAuthOrLocalTrusted_RejectsTCPWithoutJWT(t *testing.T) {
	authSvc, err := auth.NewService("pw", "jwt-secret-for-reject-test", nil)
	require.NoError(t, err)

	called := false
	handler := authOrLocalTrusted(authSvc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest("GET", "/api/tasks", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	assert.False(t, called, "handler must NOT run without local flag or JWT")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestSocketServer_EndToEnd boots the real server, opens its Unix socket,
// hits a protected route without any auth headers, and expects 200. The
// matching TCP path with the same handler returns 401 because no JWT was
// supplied. This is the regression test for the central refactor.
func TestSocketServer_EndToEnd(t *testing.T) {
	s, repo, _, _ := setupServerWithSocket(t)

	runs := []model.Run{}
	repo.On("QueryRuns", mock.Anything, storage.RunQuery{Limit: 50}).Return(runs, nil)
	repo.On("CountRunsFiltered", mock.Anything, "", "", "").Return(int64(0), nil)

	// --- TCP path: no JWT, no local flag → 401 ---
	tcpReq := httptest.NewRequest("GET", "/api/runs", nil)
	tcpW := httptest.NewRecorder()
	s.router.ServeHTTP(tcpW, tcpReq)
	assert.Equal(t, http.StatusUnauthorized, tcpW.Code, "TCP without JWT must be 401")

	// --- Socket path: local-trusted flag on context, no JWT → 200 ---
	socketReq := httptest.NewRequest("GET", "/api/runs", nil)
	socketReq = socketReq.WithContext(context.WithValue(socketReq.Context(), localTrustedKey{}, true))
	socketW := httptest.NewRecorder()
	s.router.ServeHTTP(socketW, socketReq)
	assert.Equal(t, http.StatusOK, socketW.Code, "local-trusted must skip auth")
}

// TestSocketServer_StartUnlinksSocketOnShutdown exercises the real listener
// bind/unbind flow: Start opens the socket, a smoke request goes through it,
// Shutdown removes the file. A subsequent boot would not hit "address in
// use" on a clean stop.
func TestSocketServer_StartUnlinksSocketOnShutdown(t *testing.T) {
	s, _, _, _ := setupServerWithSocket(t)
	// Pick an ephemeral port for the TCP side so multiple tests can run in
	// parallel without colliding.
	s.port = pickFreePort(t)

	socketPath := s.socketPath

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()

	requireSocketReady(t, socketPath, 2*time.Second)

	// Round-trip a health check over the socket.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 2 * time.Second,
	}
	resp, err := client.Get("http://runwisp/health")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "OK", strings.TrimSpace(string(body)))

	// Shutdown should remove the socket file.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.Shutdown(ctx))

	<-errCh // wait for Start to return

	_, err = os.Stat(socketPath)
	assert.True(t, os.IsNotExist(err), "socket file should be removed on shutdown")
}

// setupServerWithSocket is the shared boot for socket-aware tests. It mirrors
// setupServer but allocates a socket path inside a temp dir.
func setupServerWithSocket(t *testing.T) (*Server, *testutil.MockRunRepository, *testutil.MockExecutor, string) {
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

	scheduler := runtime.NewScheduler(jm, tasks, time.UTC)
	tmpDir := testutil.ShortTempDir(t)

	s, err := New(Options{
		DB:          repo,
		TaskManager: jm,
		Tasks:       tasks,
		Scheduler:   scheduler,
		Host:        "127.0.0.1",
		Port:        9477,
		SocketPath:  filepath.Join(tmpDir, "runwisp.sock"),
		LogDir:      tmpDir,
		EventBus:    eb,
		Password:    "secret",
		JWTSecret:   "test-jwt-secret",
	})
	require.NoError(t, err)
	return s, repo, exec, tmpDir
}

// requireSocketReady polls until a Unix socket accepts connections.
func requireSocketReady(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNowf(t, "socket not ready", "path=%s", path)
}

// pickFreePort grabs an ephemeral TCP port and lets it go so the caller can
// bind to it. Race window between release and bind is acceptable for tests.
func pickFreePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	_, portStr, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portStr)
	return port
}
