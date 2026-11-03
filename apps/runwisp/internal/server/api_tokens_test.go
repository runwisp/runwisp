// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"

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

// apiTokenRequest sends GET /api/tasks from ip, with a bearer token or a cookie.
func apiTokenRequest(s *Server, ip, bearer, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	req.RemoteAddr = ip + ":1234"
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// Wrong API tokens lock the client IP out of API tokens, the right one included,
// so guessing stops. Other IPs are unaffected and the lockout is logged once,
// without the token.
func TestAPIToken_FailureLimiter(t *testing.T) {
	logs := captureSlog(t)
	s := setupAPITokenServer(t)
	const guess = "guessed-api-token-0123456789abcdef"

	for range 5 * tokenMaxFailures {
		require.Equal(t, http.StatusOK, apiTokenRequest(s, "192.0.2.1", testOperatorAPIToken, "").Code,
			"a valid token never counts toward the lockout")
	}
	for range tokenMaxFailures {
		require.Equal(t, http.StatusUnauthorized, apiTokenRequest(s, "192.0.2.2", guess, "").Code)
	}
	w := apiTokenRequest(s, "192.0.2.2", guess, "")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "Too many failed API token attempts")
	assert.Equal(t, "60", w.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusTooManyRequests, apiTokenRequest(s, "192.0.2.2", testOperatorAPIToken, "").Code,
		"a locked-out IP is refused even with a valid token")
	assert.Equal(t, http.StatusOK, apiTokenRequest(s, "192.0.2.3", testViewerAPIToken, "").Code, "the lockout is per IP")

	assert.Equal(t, 1, strings.Count(logs.String(), "Too many failed API token attempts"), "one lockout, one log line")
	assert.NotContains(t, logs.String(), guess)
	assert.NotContains(t, logs.String(), testOperatorAPIToken)
}

// Stale sessions are not guesses: an expired or foreign session token, as a
// cookie or a bearer header, never counts, and a client locked out of API
// tokens keeps its session.
func TestAPIToken_FailureLimiterSparesSessions(t *testing.T) {
	s := setupAPITokenServer(t)
	expired := "1." + strings.Repeat("A", 43)
	forged := s.auth.IssueToken(auth.SessionDuration)[:11] + strings.Repeat("A", 43)

	for range 3 * tokenMaxFailures {
		require.Equal(t, http.StatusUnauthorized, apiTokenRequest(s, "192.0.2.4", "", expired).Code)
		require.Equal(t, http.StatusUnauthorized, apiTokenRequest(s, "192.0.2.4", expired, "").Code)
		require.Equal(t, http.StatusUnauthorized, apiTokenRequest(s, "192.0.2.4", forged, "").Code)
	}
	assert.Equal(t, http.StatusOK, apiTokenRequest(s, "192.0.2.4", testOperatorAPIToken, "").Code,
		"session failures don't lock out API tokens")

	for range tokenMaxFailures + 1 {
		apiTokenRequest(s, "192.0.2.5", "guessed-api-token-0123456789abcdef", "")
	}
	require.Equal(t, http.StatusTooManyRequests, apiTokenRequest(s, "192.0.2.5", testOperatorAPIToken, "").Code)
	session := s.auth.IssueToken(auth.SessionDuration)
	assert.Equal(t, http.StatusOK, apiTokenRequest(s, "192.0.2.5", "", session).Code, "cookie session still works")
	assert.Equal(t, http.StatusOK, apiTokenRequest(s, "192.0.2.5", session, "").Code, "bearer session token still works")
}
