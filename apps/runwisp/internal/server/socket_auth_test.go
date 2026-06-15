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
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
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
	s.port = testutil.PickFreePort(t)

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

// TestServer_ReadySignalsAfterListenersBound verifies Ready() closes only once
// the server is actually serving, so readiness probes don't race the bind.
func TestServer_ReadySignalsAfterListenersBound(t *testing.T) {
	s, _, _, _ := setupServerWithSocket(t)
	s.port = testutil.PickFreePort(t)

	// Not ready before Start.
	select {
	case <-s.Ready():
		t.Fatal("Ready() closed before Start()")
	default:
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()

	select {
	case <-s.Ready():
	case <-time.After(3 * time.Second):
		t.Fatal("Ready() did not close after Start()")
	}

	// Once Ready is closed the socket must actually accept connections.
	requireSocketReady(t, s.socketPath, 2*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.Shutdown(ctx))
	<-errCh
}

// setupServerWithSocket is the shared boot for socket-aware tests. It reuses
// setupServerWithOpts and adds a Unix socket bound under a short temp dir (the
// socket path must stay well under the ~104-char sun_path limit).
func setupServerWithSocket(t *testing.T) (*Server, *testutil.MockRunRepository, *testutil.MockExecutor, string) {
	sockDir := testutil.ShortTempDir(t)
	return setupServerWithOpts(t, func(o *Options) {
		o.Host = "127.0.0.1"
		o.SocketPath = filepath.Join(sockDir, "runwisp.sock")
	})
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
