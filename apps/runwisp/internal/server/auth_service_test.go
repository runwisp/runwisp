// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/datadir"
	srp "mz.attahri.com/code/srp/v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testPassword = "test-password"

// newAuthFixture builds an AuthService bound to a fresh SRP credential pair
// derived from testPassword. Returned alongside the salt so tests can stand
// up matching clients without recomputing the credentials inline.
func newAuthFixture(t *testing.T) (*AuthService, []byte) {
	t.Helper()
	salt, err := datadir.GenerateSRPSalt()
	require.NoError(t, err)
	verifier, err := datadir.DeriveSRPVerifier(testPassword, salt)
	require.NoError(t, err)
	auth, err := NewAuthService(verifier, salt, "test-jwt-secret", nil)
	require.NoError(t, err)
	return auth, salt
}

func TestNewAuthService_RejectsEmptyJWTSecret(t *testing.T) {
	salt, _ := datadir.GenerateSRPSalt()
	verifier, _ := datadir.DeriveSRPVerifier(testPassword, salt)
	_, err := NewAuthService(verifier, salt, "", nil)
	assert.Error(t, err)
}

func TestNewAuthService_RejectsEmptyVerifier(t *testing.T) {
	_, err := NewAuthService(nil, []byte("salt"), "jwt", nil)
	assert.Error(t, err)
}

func TestNewAuthService_Ok(t *testing.T) {
	auth, _ := newAuthFixture(t)
	assert.NotNil(t, auth.JWTAuth())
}

func TestSRPSessionStore_PopIsSingleUse(t *testing.T) {
	store := newSRPSessionStore()
	srv, err := srp.NewServer(datadir.SRPParams(), datadir.SRPIdentity,
		mustSalt(t), mustVerifier(t, "x", mustSalt(t)))
	require.NoError(t, err)

	store.put("sess-1", &srpSession{server: srv})
	assert.NotNil(t, store.pop("sess-1"))
	assert.Nil(t, store.pop("sess-1"), "second pop must miss")
}

func mustSalt(t *testing.T) []byte {
	t.Helper()
	s, err := datadir.GenerateSRPSalt()
	require.NoError(t, err)
	return s
}

func mustVerifier(t *testing.T, password string, salt []byte) []byte {
	t.Helper()
	v, err := datadir.DeriveSRPVerifier(password, salt)
	require.NoError(t, err)
	return v
}

// driveSRPHandshake runs the full /srp/start + /srp/finish flow against the
// supplied AuthService using the given password, and returns the response
// bodies. Tests use this to assert success and to introspect the recorder
// (cookies, statuses) after.
type srpResult struct {
	StartCode  int
	FinishCode int
	StartBody  SRPStartResponseBody
	FinishBody SRPFinishResponseBody
	FinishRec  *httptest.ResponseRecorder
}

func driveSRP(t *testing.T, auth *AuthService, password string) srpResult {
	t.Helper()

	startReq := httptest.NewRequest("POST", "/api/auth/srp/start", strings.NewReader(`{}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	auth.handleSRPStart(startRec, startReq)

	res := srpResult{StartCode: startRec.Code, FinishRec: nil}
	if startRec.Code != http.StatusOK {
		return res
	}
	require.NoError(t, json.NewDecoder(startRec.Body).Decode(&res.StartBody))

	salt, err := hex.DecodeString(res.StartBody.Salt)
	require.NoError(t, err)
	B, err := hex.DecodeString(res.StartBody.B)
	require.NoError(t, err)

	client, err := srp.NewClient(datadir.SRPParams(), datadir.SRPIdentity, password, salt)
	require.NoError(t, err)
	require.NoError(t, client.SetB(B))
	M1, err := client.ComputeM1()
	require.NoError(t, err)

	finishBody := map[string]string{
		"sessionID": res.StartBody.SessionID,
		"A":         hex.EncodeToString(client.A()),
		"M1":        hex.EncodeToString(M1),
	}
	bodyJSON, _ := json.Marshal(finishBody)

	finishReq := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader(string(bodyJSON)))
	finishReq.Header.Set("Content-Type", "application/json")
	finishRec := httptest.NewRecorder()
	auth.handleSRPFinish(finishRec, finishReq)

	res.FinishCode = finishRec.Code
	res.FinishRec = finishRec
	if finishRec.Code == http.StatusOK {
		require.NoError(t, json.NewDecoder(finishRec.Body).Decode(&res.FinishBody))
	}
	return res
}

func TestSRPHandshake_Success(t *testing.T) {
	auth, _ := newAuthFixture(t)
	res := driveSRP(t, auth, testPassword)

	require.Equal(t, http.StatusOK, res.StartCode)
	require.Equal(t, http.StatusOK, res.FinishCode)
	assert.NotEmpty(t, res.FinishBody.Token)
	assert.NotEmpty(t, res.FinishBody.M2)

	cookies := res.FinishRec.Result().Cookies()
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

func TestSRPHandshake_WrongPassword(t *testing.T) {
	auth, _ := newAuthFixture(t)
	res := driveSRP(t, auth, "wrong-password")
	assert.Equal(t, http.StatusUnauthorized, res.FinishCode)
}

func TestSRPHandshake_ServerProofVerifiesOnClient(t *testing.T) {
	auth, _ := newAuthFixture(t)

	startReq := httptest.NewRequest("POST", "/api/auth/srp/start", strings.NewReader(`{}`))
	startReq.Header.Set("Content-Type", "application/json")
	startRec := httptest.NewRecorder()
	auth.handleSRPStart(startRec, startReq)
	require.Equal(t, http.StatusOK, startRec.Code)

	var start SRPStartResponseBody
	require.NoError(t, json.NewDecoder(startRec.Body).Decode(&start))
	salt, _ := hex.DecodeString(start.Salt)
	B, _ := hex.DecodeString(start.B)

	client, err := srp.NewClient(datadir.SRPParams(), datadir.SRPIdentity, testPassword, salt)
	require.NoError(t, err)
	require.NoError(t, client.SetB(B))
	M1, _ := client.ComputeM1()

	finishBody := map[string]string{
		"sessionID": start.SessionID,
		"A":         hex.EncodeToString(client.A()),
		"M1":        hex.EncodeToString(M1),
	}
	bodyJSON, _ := json.Marshal(finishBody)
	finishReq := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader(string(bodyJSON)))
	finishReq.Header.Set("Content-Type", "application/json")
	finishRec := httptest.NewRecorder()
	auth.handleSRPFinish(finishRec, finishReq)
	require.Equal(t, http.StatusOK, finishRec.Code)

	var finish SRPFinishResponseBody
	require.NoError(t, json.NewDecoder(finishRec.Body).Decode(&finish))
	M2, err := hex.DecodeString(finish.M2)
	require.NoError(t, err)
	ok, err := client.CheckM2(M2)
	require.NoError(t, err)
	assert.True(t, ok, "client must verify the server's M2")
}

func TestSRPHandshake_ReplayedSessionIDRejected(t *testing.T) {
	auth, _ := newAuthFixture(t)

	startReq := httptest.NewRequest("POST", "/api/auth/srp/start", strings.NewReader(`{}`))
	startRec := httptest.NewRecorder()
	auth.handleSRPStart(startRec, startReq)
	require.Equal(t, http.StatusOK, startRec.Code)
	var start SRPStartResponseBody
	require.NoError(t, json.NewDecoder(startRec.Body).Decode(&start))
	salt, _ := hex.DecodeString(start.Salt)
	B, _ := hex.DecodeString(start.B)
	client, _ := srp.NewClient(datadir.SRPParams(), datadir.SRPIdentity, testPassword, salt)
	_ = client.SetB(B)
	M1, _ := client.ComputeM1()
	finishBody := map[string]string{
		"sessionID": start.SessionID,
		"A":         hex.EncodeToString(client.A()),
		"M1":        hex.EncodeToString(M1),
	}
	bodyJSON, _ := json.Marshal(finishBody)
	// First finish: success.
	req1 := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader(string(bodyJSON)))
	rec1 := httptest.NewRecorder()
	auth.handleSRPFinish(rec1, req1)
	assert.Equal(t, http.StatusOK, rec1.Code)

	// Replay: same session ID + M1, server has popped the session.
	req2 := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader(string(bodyJSON)))
	rec2 := httptest.NewRecorder()
	auth.handleSRPFinish(rec2, req2)
	assert.Equal(t, http.StatusUnauthorized, rec2.Code)
}

func TestSRPHandshake_MalformedBody(t *testing.T) {
	auth, _ := newAuthFixture(t)
	req := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader("not-json"))
	rec := httptest.NewRecorder()
	auth.handleSRPFinish(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSRPHandshake_UnknownSessionID(t *testing.T) {
	auth, _ := newAuthFixture(t)
	body := `{"sessionID":"does-not-exist","A":"deadbeef","M1":"deadbeef"}`
	req := httptest.NewRequest("POST", "/api/auth/srp/finish", strings.NewReader(body))
	rec := httptest.NewRecorder()
	auth.handleSRPFinish(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestSRPHandshake_TokenContainsExpAndIat(t *testing.T) {
	auth, _ := newAuthFixture(t)
	res := driveSRP(t, auth, testPassword)
	require.Equal(t, http.StatusOK, res.FinishCode)

	tok, err := auth.jwtAuth.Decode(res.FinishBody.Token)
	require.NoError(t, err)
	exp, ok := tok.Expiration()
	assert.True(t, ok)
	assert.False(t, exp.IsZero())
	iat, ok := tok.IssuedAt()
	assert.True(t, ok)
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
	auth, _ := newAuthFixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/srp/finish", nil)
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
	auth, _ := newAuthFixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/srp/finish", nil)
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
	salt, _ := datadir.GenerateSRPSalt()
	verifier, _ := datadir.DeriveSRPVerifier(testPassword, salt)
	auth, err := NewAuthService(verifier, salt, "test-jwt-secret", trusted)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/srp/finish", nil)
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
	auth, _ := newAuthFixture(t)

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
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"

	ctx := context.WithValue(r.Context(), peerAddrContextKey, "203.0.113.50:5678")
	r = r.WithContext(ctx)

	assert.False(t, isLoopbackRequest(r),
		"should use the real peer address from context, not the spoofed RemoteAddr")
}

func TestIsLoopbackRequest_ContextLoopbackAllowed(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.50:5678"

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

	req := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusSeeOther, w.Code)

	req = httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w = httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

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

	ticket, err := s.auth.CreateLaunchTicket()
	require.NoError(t, err)

	launchReq := httptest.NewRequest("GET", "/api/auth/launch?ticket="+ticket, nil)
	launchReq.RemoteAddr = "127.0.0.1:54321"
	launchW := httptest.NewRecorder()
	s.router.ServeHTTP(launchW, launchReq)
	require.Equal(t, http.StatusSeeOther, launchW.Code)

	var authCookie *http.Cookie
	for _, c := range launchW.Result().Cookies() {
		if c.Name == authCookieName {
			authCookie = c
			break
		}
	}
	require.NotNil(t, authCookie)

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
