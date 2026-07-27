// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupNoAuthServer builds the standard test server with RUNWISP_NO_AUTH
// semantics: auth middleware skipped, status reporting auth_required=false.
func setupNoAuthServer(t *testing.T) *Server {
	t.Helper()
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.NoAuth = true
		o.PasswordEphemeral = true
	})
	return s
}

// TestAuthStatus_NoAuth locks the contract the web UI keys off: with NoAuth
// the status endpoint reports auth_required=false so the login modal is
// skipped entirely.
func TestAuthStatus_NoAuth(t *testing.T) {
	s := setupNoAuthServer(t)

	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body AuthStatusBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.False(t, body.AuthRequired)
	assert.True(t, body.Authenticated)
}

// TestProtectedRoute_NoAuth_TCPAllowed verifies the point of the feature: an
// unauthenticated TCP request (no JWT, no cookie, not on the socket) reaches
// protected routes when NoAuth is set. The default-mode inverse — 401 for the
// same request — is covered by TestAuthFlow in server_test.go and
// socket_auth_test.go, guarding that no-auth never leaks into the default.
func TestProtectedRoute_NoAuth_TCPAllowed(t *testing.T) {
	s := setupNoAuthServer(t)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.RemoteAddr = "192.0.2.10:54321" // non-loopback TCP peer
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
