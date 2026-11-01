// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOperatorAPIToken = "operator-api-token-0123456789abcdef"
	testViewerAPIToken   = "viewer-api-token-0123456789abcdef01"
)

func setupAPITokenServer(t *testing.T) *Server {
	t.Helper()
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.APITokens = []string{testOperatorAPIToken}
		o.ViewerAPITokens = []string{testViewerAPIToken}
	})
	return s
}

func TestAPIToken_ViewerReadsButCannotWrite(t *testing.T) {
	s := setupAPITokenServer(t)

	assert.Equal(t, http.StatusOK, serve(s, http.MethodGet, "/api/tasks", testViewerAPIToken).Code)
	w := serve(s, http.MethodPost, "/api/tasks/task1/run", testViewerAPIToken)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), ReadOnlySessionMessage)
}

func TestAPIToken_OperatorReadsAndWrites(t *testing.T) {
	s := setupAPITokenServer(t)

	assert.Equal(t, http.StatusOK, serve(s, http.MethodGet, "/api/tasks", testOperatorAPIToken).Code)
	// A POST passes the read-only gate. Launch-ticket mint is used because it
	// has no side effect on a task (the test executor is a strict mock).
	assert.Equal(t, http.StatusOK, serve(s, http.MethodPost, "/api/auth/launch-ticket", testOperatorAPIToken).Code)
}

func TestAPIToken_UnknownRejected(t *testing.T) {
	s := setupAPITokenServer(t)
	assert.Equal(t, http.StatusUnauthorized, serve(s, http.MethodGet, "/api/tasks", "not-a-token").Code)
}

func TestAPIToken_SetAPITokensAppliesLive(t *testing.T) {
	s := setupAPITokenServer(t)
	s.SetAPITokens([]string{"rotated-operator-token-0123456789ab"}, nil)

	assert.Equal(t, http.StatusUnauthorized, serve(s, http.MethodGet, "/api/tasks", testOperatorAPIToken).Code)
	assert.Equal(t, http.StatusUnauthorized, serve(s, http.MethodGet, "/api/tasks", testViewerAPIToken).Code)
	assert.Equal(t, http.StatusOK, serve(s, http.MethodGet, "/api/tasks", "rotated-operator-token-0123456789ab").Code)
}

func TestAPIToken_AuthStatus(t *testing.T) {
	s := setupAPITokenServer(t)
	for token, wantReadOnly := range map[string]bool{testOperatorAPIToken: false, testViewerAPIToken: true} {
		// The status endpoint reads the session cookie, like for session tokens.
		req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
		req.AddCookie(&http.Cookie{Name: "runwisp_jwt", Value: token})
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var body AuthStatusBody
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.True(t, body.Authenticated)
		assert.Equal(t, wantReadOnly, body.ReadOnly)
	}
}
