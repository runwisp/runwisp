// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Router-level JWT verification ---

func TestProtectedRoute_RejectsTokenWithWrongAudience(t *testing.T) {
	s, _, _, _ := setupServer(t)

	_, ts, err := s.auth.JWTAuth().Encode(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": auth.JWTIssuer,
		"aud": "some-other-api",
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+ts)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestProtectedRoute_RejectsTokenWithWrongIssuer(t *testing.T) {
	s, _, _, _ := setupServer(t)

	_, ts, err := s.auth.JWTAuth().Encode(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": "someone-else",
		"aud": auth.JWTAudience,
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+ts)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Trusted-proxy parsing (server-package helper) ---

func TestParseTrustedProxies_RejectsCatchAll(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "::/0"} {
		_, err := parseTrustedProxies(cidr)
		assert.Error(t, err, "expected %s to be rejected", cidr)
	}
}

func TestParseTrustedProxies_AcceptsValidCIDR(t *testing.T) {
	opts, err := parseTrustedProxies("10.0.0.0/8,127.0.0.1")
	require.NoError(t, err)
	require.NotNil(t, opts)
	assert.Equal(t, []string{"10.0.0.0/8", "127.0.0.1/32"}, opts.AllowedSubnets)
}

// --- Local-request gating helpers ---

func TestIsLocalRequest(t *testing.T) {
	tests := []struct {
		name     string
		addr     string
		expected bool
	}{
		{"IPv4 loopback", "127.0.0.1:1234", true},
		{"IPv6 loopback", "[::1]:1234", true},
		{"External IPv4", "192.168.1.1:1234", false},
		{"External IPv6", "[2001:db8::1]:1234", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.addr
			assert.Equal(t, tt.expected, isLocalRequest(r))
		})
	}
}

func TestIsLocalRequest_UsesPeerAddrContext(t *testing.T) {
	// Simulates what happens when middleware.RealIP overwrites RemoteAddr
	// with a spoofed X-Real-IP header. The savePeerAddr middleware stores
	// the original TCP address in context, so isLocalRequest should use
	// that instead of the spoofed RemoteAddr.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234" // After RealIP spoofing

	// Context has the real peer address (set by savePeerAddr before RealIP)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "203.0.113.50:5678")
	r = r.WithContext(ctx)

	assert.False(t, isLocalRequest(r),
		"should use the real peer address from context, not the spoofed RemoteAddr")
}

func TestIsLocalRequest_ContextLoopbackAllowed(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.50:5678" // After RealIP might change it

	ctx := context.WithValue(r.Context(), peerAddrContextKey, "127.0.0.1:1234")
	r = r.WithContext(ctx)

	assert.True(t, isLocalRequest(r),
		"should allow when real peer address is loopback")
}

func TestIsLocalRequest_UnixSocketTrustedFlag(t *testing.T) {
	// A request delivered on the Unix socket carries the local-trusted flag
	// even when its synthetic RemoteAddr is non-loopback ("@" for an abstract
	// unix peer, or the socket path).
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "@"
	ctx := context.WithValue(r.Context(), localTrustedKey{}, true)
	r = r.WithContext(ctx)

	assert.True(t, isLocalRequest(r),
		"local-trusted flag must override the non-loopback peer address")
}

// --- Launch-ticket endpoint integration ---

func TestHandleLaunchTicket_Success(t *testing.T) {
	s, _, _, _ := setupServer(t)

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()

	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))

	// Verify auth cookie was set.
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
			break
		}
	}
	require.NotNil(t, cookie, "auth cookie should be set")
	assert.Equal(t, auth.CookiePath, cookie.Path)
	assert.True(t, cookie.HttpOnly)
}

func TestHandleLaunchTicket_SingleUse(t *testing.T) {
	s, _, _, _ := setupServer(t)

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	// First request succeeds.
	req := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Second request with same ticket fails.
	req = httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestHandleLaunchTicket_RedirectsToSafePath ensures the optional `redirect`
// query parameter steers the post-launch redirect at a same-origin path —
// used by the TUI's "download log" action to drop the operator straight on
// the raw-log endpoint after the cookie is set.
func TestHandleLaunchTicket_RedirectsToSafePath(t *testing.T) {
	cases := []struct {
		name       string
		redirect   string
		wantTarget string
	}{
		{name: "absolute-path", redirect: "/api/tasks/foo/runs/bar/log/raw", wantTarget: "/api/tasks/foo/runs/bar/log/raw"},
		{name: "empty-falls-back-to-root", redirect: "", wantTarget: "/"},
		{name: "scheme-relative-rejected", redirect: "//evil.example.com/x", wantTarget: "/"},
		{name: "non-absolute-rejected", redirect: "evil", wantTarget: "/"},
		{name: "external-url-rejected", redirect: "https://evil.example.com/", wantTarget: "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := setupServer(t)

			ticket, err := s.auth.CreateLaunchTicket()
			require.NoError(t, err)

			url := "/api/auth/launch?ticket=" + ticket
			if tc.redirect != "" {
				url += "&redirect=" + neturl.QueryEscape(tc.redirect)
			}
			req := httptest.NewRequest("GET", url, nil)
			req.RemoteAddr = "127.0.0.1:54321"
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusSeeOther, w.Code)
			assert.Equal(t, tc.wantTarget, w.Header().Get("Location"))
		})
	}
}

func TestHandleLaunchTicket_NonLoopbackRejected(t *testing.T) {
	s, _, _, _ := setupServer(t)

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "192.168.1.100:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleLaunchTicket_MissingTicket(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/launch", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleLaunchTicket_InvalidTicket(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/launch?ticket=bogus", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Auth status endpoint tests ---

func TestAuthStatus_Unauthenticated(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var body AuthStatusBody
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.True(t, body.AuthRequired)
	assert.False(t, body.Authenticated)
}

func TestAuthStatus_AuthenticatedViaCookie(t *testing.T) {
	s, _, _, _ := setupServer(t)

	// First, get a valid JWT by redeeming a launch ticket.
	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	launchReq := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	launchReq.RemoteAddr = "127.0.0.1:54321"
	launchW := httptest.NewRecorder()
	s.router.ServeHTTP(launchW, launchReq)
	require.Equal(t, http.StatusSeeOther, launchW.Code)

	// Extract the cookie from the launch response.
	var authCookie *http.Cookie
	for _, c := range launchW.Result().Cookies() {
		if c.Name == auth.CookieName {
			authCookie = c
			break
		}
	}
	require.NotNil(t, authCookie)

	// Now check auth status with the cookie.
	statusReq := httptest.NewRequest("GET", "/api/auth/status", nil)
	statusReq.AddCookie(authCookie)
	statusW := httptest.NewRecorder()
	s.router.ServeHTTP(statusW, statusReq)

	assert.Equal(t, http.StatusOK, statusW.Code)
	var body AuthStatusBody
	require.NoError(t, json.NewDecoder(statusW.Body).Decode(&body))
	assert.True(t, body.AuthRequired)
	assert.True(t, body.Authenticated)
}
