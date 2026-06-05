// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeSlogTo redirects slog.Default() to w for the duration of t and restores
// the previous default on cleanup. slog.Default is process-global, so tests
// using this helper must NOT run with t.Parallel against other tests that
// share the default logger.
func routeSlogTo(t *testing.T, w io.Writer) {
	t.Helper()
	h := slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// newServerForCredentialsTest builds a Server like setupServerWithSocket but
// lets the caller toggle PasswordEphemeral, which is what the handler gates
// on. Keeping it local avoids leaking yet another parameter across the
// existing setupServer / setupServerWithSocket helpers.
func newServerForCredentialsTest(t *testing.T, password string, ephemeral bool) *Server {
	t.Helper()

	repo := new(testutil.MockRunRepository)
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := runtime.NewTaskManager(exec, eb, time.Now)
	tasks := map[string]*model.Task{}
	scheduler := runtime.NewScheduler(jm, tasks, time.UTC)
	tmpDir := t.TempDir()

	s, err := New(Options{
		DB:                repo,
		TaskManager:       jm,
		Tasks:             tasks,
		Scheduler:         scheduler,
		Host:              "127.0.0.1",
		Port:              9477,
		LogDir:            tmpDir,
		EventBus:          eb,
		Password:          password,
		PasswordEphemeral: ephemeral,
		JWTSecret:         "test-jwt-secret",
	})
	require.NoError(t, err)
	return s
}

func TestLocalCredentials_SocketEphemeralReturnsPassword(t *testing.T) {
	s := newServerForCredentialsTest(t, "in-memory-pw", true)

	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))

	var body LocalCredentialsBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "in-memory-pw", body.Password)
	assert.True(t, body.Ephemeral)
}

func TestLocalCredentials_SocketEnvVarReturns404(t *testing.T) {
	s := newServerForCredentialsTest(t, "env-pw", false)

	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Body.String(), "env-pw",
		"the env-var password must never appear in the response body")
}

// TestLocalCredentials_NoAuthReturns409 guards the RUNWISP_NO_AUTH contract:
// the internally-minted password gates nothing, so disclosing it would
// present a non-credential as a credential. 409 is distinct from the env-var
// 404 so `runwisp password` can print the right explanation for each.
func TestLocalCredentials_NoAuthReturns409(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.NoAuth = true
		o.PasswordEphemeral = true
	})

	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.NotContains(t, w.Body.String(), "secret",
		"the minted password must never appear in the response body")
}

func TestLocalCredentials_TCPWithJWTReturns403(t *testing.T) {
	s := newServerForCredentialsTest(t, "in-memory-pw", true)

	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	addAuth(req, s)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"a valid JWT on a TCP request must NOT reach the credentials endpoint")
	assert.NotContains(t, w.Body.String(), "in-memory-pw",
		"the ephemeral password must never appear when 403 is returned")
}

func TestLocalCredentials_TCPNoJWTReturns401(t *testing.T) {
	s := newServerForCredentialsTest(t, "in-memory-pw", true)

	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"the authOrLocalTrusted middleware must reject unauthenticated TCP callers before the handler runs")
}

// TestLocalCredentials_LogAuditExcludesPasswordValue locks the audit-log
// contract: the daemon must record THAT the ephemeral password was retrieved,
// but must never log the password VALUE next to the audit message. A future
// change that "helpfully" attaches the password as a structured field — e.g.
// `slog.Info("retrieved", "pw", srv.auth.Password())` — would leak it through
// any log shipper subscribed to slog output. This test fails before that
// regression lands.
func TestLocalCredentials_LogAuditExcludesPasswordValue(t *testing.T) {
	t.Run("success path logs audit message, never the value", func(t *testing.T) {
		const sentinel = "DistinctSentinel-9zX"
		var buf bytes.Buffer
		routeSlogTo(t, &buf)

		s := newServerForCredentialsTest(t, sentinel, true)
		req := httptest.NewRequest("GET", "/api/local/credentials", nil)
		req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		assert.Contains(t, buf.String(), "Ephemeral password retrieved via socket",
			"the audit-trail message must be recorded for operators to observe")
		assert.NotContains(t, buf.String(), sentinel,
			"the password value must never reach the log buffer alongside the audit message")
	})

	t.Run("env-var refusal path also stays silent about the value", func(t *testing.T) {
		const sentinel = "DoNotLog-Operator-Supplied"
		var buf bytes.Buffer
		routeSlogTo(t, &buf)

		s := newServerForCredentialsTest(t, sentinel, false)
		req := httptest.NewRequest("GET", "/api/local/credentials", nil)
		req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)

		assert.NotContains(t, buf.String(), sentinel,
			"the 404 branch must not start logging the operator-supplied password value")
	})
}

func TestIsLocalTrustedCtx(t *testing.T) {
	assert.False(t, IsLocalTrustedCtx(context.Background()))
	ctx := context.WithValue(context.Background(), localTrustedKey{}, true)
	assert.True(t, IsLocalTrustedCtx(ctx))
	// Non-true values do not flip the flag.
	ctx = context.WithValue(context.Background(), localTrustedKey{}, false)
	assert.False(t, IsLocalTrustedCtx(ctx))
}
