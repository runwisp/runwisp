// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// viewerToken signs in with the read-only password and returns the session token.
func viewerToken(t *testing.T, s *Server, password string) string {
	t.Helper()
	w := chapAttempt(t, s, password)
	require.Equal(t, http.StatusOK, w.Code)
	var resp AuthLoginBody
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp.Token
}

func serve(s *Server, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func TestViewerSession_ReadsAllowedWritesRejected(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.ViewerPassword = "view-only" })
	token := viewerToken(t, s, "view-only")

	assert.Equal(t, http.StatusOK, serve(s, http.MethodGet, "/api/tasks", token).Code)

	// Every mutation is refused, including the ones that would otherwise hand a
	// viewer more access: reload and launch-ticket mint (a full-session ticket).
	for _, path := range []string{
		"/api/tasks/task1/run",
		"/api/tasks/task1/stop",
		"/api/tasks/task1/restart",
		"/api/tasks/task1/pause",
		"/api/runs/bulk/delete",
		"/api/daemon/reload",
		"/api/auth/launch-ticket",
		"/api/notifications/read",
	} {
		w := serve(s, http.MethodPost, path, token)
		assert.Equal(t, http.StatusForbidden, w.Code, path)
		assert.Contains(t, w.Body.String(), ReadOnlySessionMessage, path)
	}
	assert.Equal(t, http.StatusForbidden, serve(s, http.MethodDelete, "/api/runs/abc", token).Code)
}

func TestViewerSession_AdminUnaffected(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.ViewerPassword = "view-only" })
	var resp AuthLoginBody
	require.NoError(t, json.Unmarshal(chapAttempt(t, s, "secret").Body.Bytes(), &resp))
	assert.False(t, resp.ReadOnly)

	// The admin passes the read-only gate; whatever the handler answers, it is
	// not the viewer 403.
	w := serve(s, http.MethodPost, "/api/auth/launch-ticket", resp.Token)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestViewerPassword_OffByDefault(t *testing.T) {
	s, _, _, _ := setupServer(t)
	assert.Equal(t, http.StatusUnauthorized, chapAttempt(t, s, "view-only").Code)
}

func TestAuthStatus_ReportsReadOnly(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.ViewerPassword = "view-only" })

	status := func(token string) AuthStatusBody {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
		req.AddCookie(&http.Cookie{Name: "runwisp_jwt", Value: token})
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		var body AuthStatusBody
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}

	got := status(viewerToken(t, s, "view-only"))
	assert.True(t, got.Authenticated)
	assert.True(t, got.ReadOnly)
	got = status(viewerToken(t, s, "secret"))
	assert.True(t, got.Authenticated)
	assert.False(t, got.ReadOnly)
	assert.False(t, status("").Authenticated)
}

func viewerPasswordRequest(s *Server, local bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/local/viewer-password", nil)
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	} else {
		addAuth(req, s)
		req.RemoteAddr = "127.0.0.1:54321"
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

func TestMintViewerPassword_SocketMintsOnceAndEnablesLogin(t *testing.T) {
	s := newServerForCredentialsTest(t, "admin-pw", true)

	var first, second LocalCredentialsBody
	w := viewerPasswordRequest(s, true)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &first))
	require.NoError(t, json.Unmarshal(viewerPasswordRequest(s, true).Body.Bytes(), &second))
	assert.NotEmpty(t, first.Password)
	assert.Equal(t, first.Password, second.Password)

	assert.Equal(t, http.StatusForbidden,
		serve(s, http.MethodPost, "/api/tasks/task1/run", viewerToken(t, s, first.Password)).Code)
}

func TestMintViewerPassword_Gates(t *testing.T) {
	t.Run("env-supplied is never disclosed", func(t *testing.T) {
		s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.ViewerPassword = "from-env" })
		w := viewerPasswordRequest(s, true)
		assert.Equal(t, http.StatusNotFound, w.Code)
		assert.NotContains(t, w.Body.String(), "from-env")
	})
	t.Run("auth off", func(t *testing.T) {
		s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.NoAuth = true })
		assert.Equal(t, http.StatusConflict, viewerPasswordRequest(s, true).Code)
	})
	t.Run("TCP session is refused", func(t *testing.T) {
		s := newServerForCredentialsTest(t, "admin-pw", true)
		assert.Equal(t, http.StatusForbidden, viewerPasswordRequest(s, false).Code)
	})
}
