// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
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
	const freshToken = "fresh-jwt-token"
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/health":
			w.WriteHeader(http.StatusOK)

		case r.URL.Path == "/api/auth/challenge":
			_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "test-nonce"})

		case r.URL.Path == "/api/auth":
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

		case strings.HasPrefix(r.URL.Path, "/api/tasks/") && strings.Contains(r.URL.Path, "/runs/"):
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

	code, err := runExecViaRemote("backup", srv.URL, "pw", false)
	require.NoError(t, err)
	assert.Equal(t, 7, code, "exit code must propagate from the failed run")
	assert.Equal(t, int32(1), stub.streamHits.Load(), "the log stream must be followed")
	assert.Equal(t, int32(1), stub.authCount.Load(), "exactly one CHAP handshake")

	// The minted token is cached for reuse.
	assert.Equal(t, "fresh-jwt-token", loadCachedToken(srv.URL))
}

func TestRunExecViaRemote_SuccessReturnsZero(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	code, err := runExecViaRemote("backup", srv.URL, "pw", false)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
}

func TestRunExecViaRemote_ReauthOnExpiredToken(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{exitCode: 0, endReason: model.ReasonSuccess, staleToken: "stale-jwt"}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	// Seed a cached (stale) token; the first trigger 401s and we re-handshake.
	storeCachedToken(srv.URL, "stale-jwt")

	code, err := runExecViaRemote("backup", srv.URL, "pw", false)
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

	code, err := runExecViaRemote("backup", srv.URL, "pw", true)
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, int32(0), stub.streamHits.Load(), "--detach must not follow the log stream")
}

func TestRunExecViaRemote_MissingPassword(t *testing.T) {
	useTempCacheDir(t)
	stub := &remoteDaemonStub{}
	srv := httptest.NewServer(stub.handler(t))
	defer srv.Close()

	_, err := runExecViaRemote("backup", srv.URL, "", false)
	require.Error(t, err)
	ufe, ok := isUserFacing(err)
	require.True(t, ok, "missing password must be a user-facing error")
	assert.Contains(t, ufe.Error(), "password is required")
}

func TestRunExecViaRemote_Unreachable(t *testing.T) {
	useTempCacheDir(t)
	// A closed server's URL is unroutable, so HealthCheck fails to dial.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	_, err := runExecViaRemote("backup", url, "pw", false)
	require.Error(t, err)
	ufe, ok := isUserFacing(err)
	require.True(t, ok)
	assert.Contains(t, ufe.Error(), "not reachable")
}
