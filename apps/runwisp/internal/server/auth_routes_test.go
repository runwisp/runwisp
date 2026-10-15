// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/httprate"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Router-level session-token verification ---

// TestProtectedRoute_SessionToken covers where the gate reads the token from;
// what makes a token valid is covered by auth.TestValidToken.
func TestProtectedRoute_SessionToken(t *testing.T) {
	s, _, _, _ := setupServer(t)

	tests := []struct {
		name string
		set  func(*http.Request)
		want int
	}{
		{"valid bearer", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+s.auth.IssueToken(time.Hour))
		}, http.StatusOK},
		{"valid cookie", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: s.auth.IssueToken(time.Hour)})
		}, http.StatusOK},
		{"invalid bearer does not fall back to a valid cookie", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer garbage")
			r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: s.auth.IssueToken(time.Hour)})
		}, http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/tasks", nil)
			tt.set(req)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)
			assert.Equal(t, tt.want, w.Code)
		})
	}
}

// --- Trusted-proxy parsing (server-package helper) ---

func TestParseTrustedProxies_RejectsCatchAll(t *testing.T) {
	for _, cidr := range []string{"0.0.0.0/0", "::/0"} {
		_, err := parseTrustedProxies([]string{cidr})
		assert.Error(t, err, "expected %s to be rejected", cidr)
	}
}

func TestParseTrustedProxies_AcceptsValidCIDR(t *testing.T) {
	opts, err := parseTrustedProxies([]string{"10.0.0.0/8", "127.0.0.1"})
	require.NoError(t, err)
	require.Len(t, opts, 2)
	assert.Equal(t, "127.0.0.1/32", opts[1].String())
}

// --- Local-request gating helpers ---

func TestIsLocalCtx(t *testing.T) {
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
			ctx := context.WithValue(context.Background(), peerAddrContextKey, tt.addr)
			assert.Equal(t, tt.expected, isLocalCtx(ctx))
		})
	}
}

func TestIsLocalCtx_UsesPeerAddrContext(t *testing.T) {
	// Simulates what happens when the XFF middleware overwrites RemoteAddr from
	// a spoofed X-Real-IP header. savePeerAddr stored the original TCP address
	// in context first, and isLocalCtx reads only that.
	ctx := context.WithValue(context.Background(), peerAddrContextKey, "203.0.113.50:5678")

	assert.False(t, isLocalCtx(ctx),
		"should use the real peer address from context, not the spoofed RemoteAddr")
}

func TestIsLocalCtx_UnixSocketTrustedFlag(t *testing.T) {
	// A request delivered on the Unix socket carries the local-trusted flag
	// even when its synthetic peer address is non-loopback ("@" for an abstract
	// unix peer, or the socket path).
	ctx := context.WithValue(context.Background(), peerAddrContextKey, "@")
	ctx = context.WithValue(ctx, localTrustedKey{}, true)

	assert.True(t, isLocalCtx(ctx),
		"local-trusted flag must override the non-loopback peer address")
}

func TestIsLocalCtx_ProxiedLoopbackRejected(t *testing.T) {
	// A same-host reverse proxy relaying an internet client connects from
	// loopback, so loopback alone must not count as local.
	ctx := context.WithValue(context.Background(), peerAddrContextKey, "127.0.0.1:1234")
	ctx = context.WithValue(ctx, proxiedContextKey, true)

	assert.False(t, isLocalCtx(ctx),
		"a proxy-relayed request must not be treated as local even from loopback")
}

func TestIsProxiedRequest(t *testing.T) {
	for _, header := range forwardedHeaders {
		t.Run(header, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set(header, "203.0.113.9")
			assert.True(t, isProxiedRequest(r, nil))
		})
	}

	t.Run("direct request", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		assert.False(t, isProxiedRequest(r, nil),
			"the local launcher probe sends no hop headers and must stay local")
	})

	t.Run("trusted proxy peer without headers", func(t *testing.T) {
		opts, err := parseTrustedProxies([]string{"127.0.0.1"})
		require.NoError(t, err)
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		assert.True(t, isProxiedRequest(r, opts))
	})
}

// --- Launch-ticket endpoint integration ---

func TestHandleLaunchTicket_Success(t *testing.T) {
	s, _, _, _ := setupServer(t)

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
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
	req := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)

	// Second request with same ticket fails.
	req = httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
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
		{name: "absolute-path", redirect: "/api/runs/bar/log/raw", wantTarget: "/api/runs/bar/log/raw"},
		{name: "empty-falls-back-to-root", redirect: "", wantTarget: "/"},
		{name: "scheme-relative-rejected", redirect: "//evil.example.com/x", wantTarget: "/"},
		{name: "non-absolute-rejected", redirect: "evil", wantTarget: "/"},
		{name: "external-url-rejected", redirect: "https://evil.example.com/", wantTarget: "/"},
		// Browsers normalize "\" to "/" in a Location header, so these would
		// become "//evil.example.com" (an open redirect) if url.Parse's empty
		// Host were trusted. They must be rejected.
		{name: "backslash-scheme-relative-rejected", redirect: "/\\evil.example.com", wantTarget: "/"},
		{name: "backslash-slash-rejected", redirect: "/\\/evil.example.com", wantTarget: "/"},
		{name: "encoded-backslash-rejected", redirect: "/%5Cevil.example.com", wantTarget: "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _, _ := setupServer(t)

			ticket, err := s.auth.CreateLaunchTicket()
			require.NoError(t, err)

			url := "/api/auth/launch-ticket?ticket=" + ticket
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

// TestHandleLaunchTicket_NonLoopbackRedeems covers cross-origin redeem: a
// remote operator's browser may redeem a valid ticket against a daemon over
// the network. The ticket itself — single-use, short-TTL, mintable only by an
// authenticated session — is the protection, not the origin.
func TestHandleLaunchTicket_NonLoopbackRedeems(t *testing.T) {
	s, _, _, _ := setupServer(t)

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
	req.RemoteAddr = "192.168.1.100:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	// A session cookie must be set so the redirected page is authenticated.
	var authCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == auth.CookieName {
			authCookie = c
		}
	}
	assert.NotNil(t, authCookie, "redeem must set a session cookie")
}

// TestHandleCreateLaunchTicket_LoopbackSucceeds covers the happy path: a
// loopback request authenticated via the local-trusted middleware mints a
// fresh single-use launch ticket.
func TestHandleCreateLaunchTicket_LoopbackSucceeds(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("POST", "/api/auth/launch-ticket", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	// The protected-routes middleware allows local-trusted requests without
	// session token, so we must mark this one as such.
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.NotEmpty(t, body["ticket"], "expected a non-empty ticket field in response")
}

// TestHandleCreateLaunchTicket_NonLoopbackAuthenticatedSucceeds covers remote
// minting: a request from a non-local origin that carries a valid session token (an
// already-authenticated session, e.g. a remote TUI that logged in via CHAP)
// may mint a launch ticket for its browser.
func TestHandleCreateLaunchTicket_NonLoopbackAuthenticatedSucceeds(t *testing.T) {
	s, _, _, _ := setupServer(t)

	// Mint a valid session cookie via the launch path.
	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)
	launchReq := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
	launchReq.RemoteAddr = "127.0.0.1:54321"
	launchW := httptest.NewRecorder()
	s.router.ServeHTTP(launchW, launchReq)
	require.Equal(t, http.StatusSeeOther, launchW.Code)
	var authCookie *http.Cookie
	for _, c := range launchW.Result().Cookies() {
		if c.Name == auth.CookieName {
			authCookie = c
			break
		}
	}
	require.NotNil(t, authCookie)

	// Re-use the session cookie from a non-loopback address — the authenticated
	// session is the mint gate, not the origin.
	req := httptest.NewRequest("POST", "/api/auth/launch-ticket", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	req.AddCookie(authCookie)

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"a valid session token from a remote origin must be allowed to mint")
	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.NotEmpty(t, body["ticket"])
}

// TestHandleCreateLaunchTicket_NonLoopbackUnauthenticatedRejected confirms the
// mint gate still holds: a non-local request with no session is rejected by the
// protected-group middleware before reaching the handler.
func TestHandleCreateLaunchTicket_NonLoopbackUnauthenticatedRejected(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("POST", "/api/auth/launch-ticket", nil)
	req.RemoteAddr = "192.168.1.100:54321"

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"an unauthenticated remote request must not be able to mint a ticket")
}

func TestHandleLaunchTicket_MissingTicket(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/launch-ticket", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	// huma rejects a missing required query param before the handler runs.
	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHandleLaunchTicket_InvalidTicket(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket=bogus", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// --- Auth rate-limit bypass regression ---

// TestAuthRateLimit_NotBypassableViaForwardedHeader is a regression test for a
// brute-force-protection bypass. chi's middleware.RealIP rewrites r.RemoteAddr
// from client-controlled X-Forwarded-For / X-Real-IP / True-Client-IP headers,
// and the login throttle keys off the resolved client IP. If RealIP is
// in the chain, a single attacker can rotate those headers to land every login
// probe in its own per-IP bucket and never trip the limiter — defeating the
// only online brute-force protection on the CHAP endpoint. With no trusted
// proxy configured, the limiter must key off the true TCP peer and ignore the
// headers, so a burst from one peer trips the limit regardless of header value.
func TestAuthRateLimit_NotBypassableViaForwardedHeader(t *testing.T) {
	s, _, _, _ := setupServer(t)
	seedWrongPasswords(s, "203.0.113.9", 2*freeLoginFailures)

	req := badLoginRequest("203.0.113.9:44444") // the throttled attacker's own TCP connection
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	req.Header.Set("X-Real-IP", "10.0.0.1")
	req.Header.Set("True-Client-IP", "10.0.0.1")

	assert.Equal(t, http.StatusTooManyRequests, serveReq(s, req).Code,
		"X-Forwarded-For/X-Real-IP must not mint a fresh throttle bucket")
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

	// First, get a valid session token by redeeming a launch ticket.
	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	launchReq := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
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

// Behind a trusted proxy the limiter must key off the hop the proxy appended,
// not the client-controlled left end of X-Forwarded-For.
func TestAuthRateLimit_NotBypassableViaSpoofedXFFBehindTrustedProxy(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) { o.TrustedProxies = []string{"10.0.0.0/8"} })

	seedWrongPasswords(s, "203.0.113.9", 2*freeLoginFailures)

	req := badLoginRequest("10.0.0.1:44444")
	req.Header.Set("X-Forwarded-For", "198.51.100.1, 203.0.113.9")
	assert.Equal(t, http.StatusTooManyRequests, serveReq(s, req).Code)
}

// badLoginRequest is a login from addr with an unknown nonce, which never
// tests the password and so never counts toward the throttle.
func badLoginRequest(addr string) *http.Request {
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"nonce":"bogus","response":"bogus"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = addr
	return req
}

func serveReq(s *Server, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w
}

// seedWrongPasswords records n wrong passwords for ip, the last one just now,
// straight into the throttle, skipping the deliberately slow CHAP key derivation.
func seedWrongPasswords(s *Server, ip string, n int) {
	s.logins.ips[httprate.CanonicalizeIP(ip)] = &loginState{failures: n, lastFail: s.logins.now()}
}

// httptest's default RemoteAddr, which chapAttempt uses.
const defaultTestIP = "192.0.2.1"

// Regression: the limiter used to count every challenge, login and ticket
// redemption, so a third sign-in or a sixth "open in browser" within five
// minutes got 429. Successful logins and redemptions must never be throttled,
// and neither must bad tickets or nonces, which never test the password.
func TestAuthRateLimit_OnlyWrongPasswordsCount(t *testing.T) {
	s, _, _, _ := setupServer(t)

	for range 3 * freeLoginFailures {
		ticket, err := s.auth.CreateLaunchTicket()
		require.NoError(t, err)
		require.Equal(t, http.StatusSeeOther, serveReq(s, httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)).Code)
		require.Equal(t, http.StatusUnauthorized, serveReq(s, httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket=bogus", nil)).Code)
		require.Equal(t, http.StatusUnauthorized, serveReq(s, badLoginRequest(defaultTestIP+":1234")).Code)
	}
	for range 3 {
		require.Equal(t, http.StatusOK, chapAttempt(t, s, "secret").Code)
	}

	require.Equal(t, http.StatusUnauthorized, chapAttempt(t, s, "wrong").Code)
	assert.Equal(t, 1, s.logins.ips[defaultTestIP].failures, "a wrong password counts")
}

// Past the free failures the right password is refused too until the delay
// passes (otherwise a guesser would learn when it hit), the 429 says how long
// to wait, other IPs are unaffected, and a later success forgets the failures.
func TestAuthRateLimit_ThrottlesAfterFreeFailures(t *testing.T) {
	s, _, _, _ := setupServer(t)
	now := time.Now()
	s.logins.now = func() time.Time { return now }
	seedWrongPasswords(s, defaultTestIP, freeLoginFailures)

	w := chapAttempt(t, s, "secret")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Equal(t, "2", w.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusUnauthorized, serveReq(s, badLoginRequest("198.51.100.7:1")).Code, "the throttle is per IP")

	now = now.Add(2 * time.Second)
	require.Equal(t, http.StatusOK, chapAttempt(t, s, "secret").Code)
	assert.NotContains(t, s.logins.ips, defaultTestIP, "a success forgets the failures")
}

func TestAuthChallenge_FloodCapped(t *testing.T) {
	s, _, _, _ := setupServer(t)
	for range maxChallengesPerMinute {
		chapChallenge(t, s)
	}
	assert.Equal(t, http.StatusTooManyRequests, serveReq(s, httptest.NewRequest("GET", "/api/auth/challenge", nil)).Code)
}

// Proxy headers from a peer outside trusted_proxies usually mean a proxy that
// isn't configured, which makes every client share one throttle bucket.
func TestResolveClientIP_WarnsOnceAboutUntrustedProxy(t *testing.T) {
	s, _, _, _ := setupServer(t)
	logs := captureSlog(t)

	plain := httptest.NewRequest("GET", "/health", nil)
	serveReq(s, plain)
	assert.NotContains(t, logs.String(), "trusted_proxies")

	for range 2 {
		req := httptest.NewRequest("GET", "/health", nil)
		req.Header.Set("X-Forwarded-For", "198.51.100.1")
		serveReq(s, req)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "Request has proxy headers"))
}
