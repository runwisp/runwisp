// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAuthService_PanicsOnEmptySecret(t *testing.T) {
	assert.Panics(t, func() {
		NewAuthService("pass", "", nil)
	})
}

func TestNewAuthService_ExplicitSecret(t *testing.T) {
	auth := NewAuthService("pass", "my-secret", nil)
	assert.NotNil(t, auth.JWTAuth())
}

func TestNonceStore_CreateAndConsume(t *testing.T) {
	store := newNonceStore()

	nonce, err := store.create()
	require.NoError(t, err)
	assert.Len(t, nonce, 64) // 32 bytes hex-encoded

	// First consume succeeds
	assert.True(t, store.consume(nonce))
	// Second consume fails (single-use)
	assert.False(t, store.consume(nonce))
}

func TestNonceStore_InvalidNonce(t *testing.T) {
	store := newNonceStore()
	assert.False(t, store.consume("nonexistent"))
}

func TestNonceStore_MultipleConcurrentNonces(t *testing.T) {
	store := newNonceStore()

	nonce1, err := store.create()
	require.NoError(t, err)
	nonce2, err := store.create()
	require.NoError(t, err)
	assert.NotEqual(t, nonce1, nonce2)

	// Both should be independently consumable
	assert.True(t, store.consume(nonce1))
	assert.True(t, store.consume(nonce2))
}

func computeChallenge(password, nonce string) string {
	h := sha256.Sum256([]byte(password + ":" + nonce))
	return hex.EncodeToString(h[:])
}

func TestHandleAuth_Success(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	nonce, err := auth.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("secret", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])

	// Verify cookie is set
	cookies := w.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == authCookieName {
			found = c
			break
		}
	}
	require.NotNil(t, found, "auth cookie should be set")
	assert.Equal(t, authCookiePath, found.Path)
	assert.True(t, found.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, found.SameSite)
	assert.Equal(t, int(JWTTokenDuration.Seconds()), found.MaxAge)
}

func TestHandleAuth_WrongPassword(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	nonce, err := auth.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("wrong-password", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid password")
}

func TestHandleAuth_InvalidNonce(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	body := `{"nonce":"deadbeef","response":"anything"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid or expired challenge")
}

func TestHandleAuth_NonceReplay(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	nonce, err := auth.nonces.create()
	require.NoError(t, err)
	response := computeChallenge("secret", nonce)

	// First attempt should succeed
	body := `{"nonce":"` + nonce + `","response":"` + response + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	auth.handleAuth(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Replay the same nonce — should fail
	req = httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	auth.handleAuth(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleAuth_MalformedBody(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAuth_OversizedBody(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	// MaxRequestBodySize is 1024 bytes; send a body larger than that
	bigBody := strings.Repeat("x", MaxRequestBodySize+100)
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(bigBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleAuth_JWTContainsExpAndIat(t *testing.T) {
	auth := NewAuthService("secret", "test-jwt-secret", nil)

	nonce, err := auth.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("secret", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	auth.handleAuth(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	// Decode the token to verify it has exp and iat
	tok, err := auth.jwtAuth.Decode(resp["token"])
	require.NoError(t, err)
	exp, ok := tok.Expiration()
	assert.True(t, ok, "token should have expiration")
	assert.False(t, exp.IsZero())
	iat, ok := tok.IssuedAt()
	assert.True(t, ok, "token should have issued-at")
	assert.False(t, iat.IsZero())
}

func TestProtectedRoute_RejectsTokenWithWrongAudience(t *testing.T) {
	s, _, _, _ := setupServer(t)

	_, ts, err := s.auth.JWTAuth().Encode(map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"iss": JWTIssuer,
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
		"aud": JWTAudience,
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+ts)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestSetAuthCookie_HTTP(t *testing.T) {
	auth := NewAuthService("pass", "test-jwt-secret", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	auth.setAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)

	c := cookies[0]
	assert.Equal(t, authCookieName, c.Name)
	assert.Equal(t, "test-token", c.Value)
	assert.Equal(t, authCookiePath, c.Path)
	assert.True(t, c.HttpOnly)
	assert.False(t, c.Secure, "Secure should be false for plain HTTP")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
}

func TestSetAuthCookie_XForwardedProtoFromUntrustedClientIgnored(t *testing.T) {
	// With no trusted proxies configured, X-Forwarded-Proto must be ignored
	// — otherwise any client could spoof "https" and trick us into setting
	// the Secure flag on a cookie that travels in cleartext.
	auth := NewAuthService("pass", "test-jwt-secret", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	r.RemoteAddr = "203.0.113.50:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	auth.setAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].Secure, "Secure must not be set based on a spoofable header")
}

func TestSetAuthCookie_XForwardedProtoFromTrustedProxyHonored(t *testing.T) {
	trusted, err := parseTrustedProxies("127.0.0.1/32")
	require.NoError(t, err)
	auth := NewAuthService("pass", "test-jwt-secret", trusted)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	auth.setAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure, "Secure should be set when XFP=https is forwarded by a trusted proxy")
}

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

// --- Launch ticket store tests ---

func TestLaunchTicketStore_CreateAndConsume(t *testing.T) {
	store := newLaunchTicketStore()

	ticket, err := store.create()
	require.NoError(t, err)
	assert.Len(t, ticket, 43) // 43 base62 chars ≈ 256 bits of entropy

	assert.True(t, store.consume(ticket))
	// Single-use: second consume fails.
	assert.False(t, store.consume(ticket))
}

func TestLaunchTicketStore_InvalidTicket(t *testing.T) {
	store := newLaunchTicketStore()
	assert.False(t, store.consume("nonexistent"))
}

func TestAuthService_CreateLaunchTicket(t *testing.T) {
	auth := NewAuthService("pass", "test-jwt-secret", nil)

	ticket, err := auth.CreateLaunchTicket()
	require.NoError(t, err)
	assert.Len(t, ticket, 43)

	// Ticket should be consumable through the internal store.
	assert.True(t, auth.launchTickets.consume(ticket))
	assert.False(t, auth.launchTickets.consume(ticket))
}

func TestIsLoopbackRequest(t *testing.T) {
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
			assert.Equal(t, tt.expected, isLoopbackRequest(r))
		})
	}
}

func TestIsLoopbackRequest_UsesPeerAddrContext(t *testing.T) {
	// Simulates what happens when middleware.RealIP overwrites RemoteAddr
	// with a spoofed X-Real-IP header. The savePeerAddr middleware stores
	// the original TCP address in context, so isLoopbackRequest should use
	// that instead of the spoofed RemoteAddr.
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234" // After RealIP spoofing

	// Context has the real peer address (set by savePeerAddr before RealIP)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "203.0.113.50:5678")
	r = r.WithContext(ctx)

	assert.False(t, isLoopbackRequest(r),
		"should use the real peer address from context, not the spoofed RemoteAddr")
}

func TestIsLoopbackRequest_ContextLoopbackAllowed(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.50:5678" // After RealIP might change it

	ctx := context.WithValue(r.Context(), peerAddrContextKey, "127.0.0.1:1234")
	r = r.WithContext(ctx)

	assert.True(t, isLoopbackRequest(r),
		"should allow when real peer address is loopback")
}

// --- Launch ticket endpoint integration tests ---

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
		if c.Name == authCookieName {
			cookie = c
			break
		}
	}
	require.NotNil(t, cookie, "auth cookie should be set")
	assert.Equal(t, authCookiePath, cookie.Path)
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
		if c.Name == authCookieName {
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
