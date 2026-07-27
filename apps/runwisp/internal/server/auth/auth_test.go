// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/chap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewService_RejectsEmptySecret(t *testing.T) {
	svc, err := NewService("pass", "", nil)
	assert.ErrorIs(t, err, ErrEmptyJWTSecret)
	assert.Nil(t, svc)
}

func TestNewService_ExplicitSecret(t *testing.T) {
	svc, err := NewService("pass", "my-secret", nil)
	require.NoError(t, err)
	assert.NotNil(t, svc.JWTAuth())
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

	assert.True(t, store.consume(nonce1))
	assert.True(t, store.consume(nonce2))
}

func computeChallenge(password, nonce string) string {
	return chap.Response(password, nonce)
}

func newServiceOrFail(t *testing.T, password string, trusted TrustedProxyChecker) *Service {
	t.Helper()
	svc, err := NewService(password, "test-jwt-secret", trusted)
	require.NoError(t, err)
	return svc
}

func TestHandleLogin_Success(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("secret", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	svc.HandleLogin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp["token"])

	// Verify cookie is set
	var found *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			found = c
			break
		}
	}
	require.NotNil(t, found, "auth cookie should be set")
	assert.Equal(t, CookiePath, found.Path)
	assert.True(t, found.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, found.SameSite)
	assert.Equal(t, int(JWTTokenDuration.Seconds()), found.MaxAge)
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("wrong-password", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid password")
}

func TestHandleLogin_InvalidNonce(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	body := `{"nonce":"deadbeef","response":"anything"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid or expired challenge")
}

func TestHandleLogin_NonceReplay(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)
	response := computeChallenge("secret", nonce)

	// First attempt should succeed
	body := `{"nonce":"` + nonce + `","response":"` + response + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Replay the same nonce — should fail
	req = httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	w = httptest.NewRecorder()
	svc.HandleLogin(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleLogin_MalformedBody(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader("not-json"))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleLogin_OversizedBody(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	// MaxRequestBodySize is 1024 bytes; send a body larger than that
	bigBody := strings.Repeat("x", MaxRequestBodySize+100)
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(bigBody))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleLogin_JWTContainsExpAndIat(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	body := `{"nonce":"` + nonce + `","response":"` + computeChallenge("secret", nonce) + `"}`
	req := httptest.NewRequest("POST", "/api/auth", strings.NewReader(body))
	w := httptest.NewRecorder()
	svc.HandleLogin(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	tok, err := svc.jwtAuth.Decode(resp["token"])
	require.NoError(t, err)
	exp, ok := tok.Expiration()
	assert.True(t, ok, "token should have expiration")
	assert.False(t, exp.IsZero())
	iat, ok := tok.IssuedAt()
	assert.True(t, ok, "token should have issued-at")
	assert.False(t, iat.IsZero())
}

func TestSetAuthCookie_HTTP(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	svc.SetAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)

	c := cookies[0]
	assert.Equal(t, CookieName, c.Name)
	assert.Equal(t, "test-token", c.Value)
	assert.Equal(t, CookiePath, c.Path)
	assert.True(t, c.HttpOnly)
	assert.False(t, c.Secure, "Secure should be false for plain HTTP")
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
}

func TestSetAuthCookie_DirectTLSIsSecure(t *testing.T) {
	// A request delivered over TLS (r.TLS != nil) sets Secure with no proxy
	// involved — this is the free win once the daemon serves HTTPS directly.
	svc := newServiceOrFail(t, "pass", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	r.TLS = &tls.ConnectionState{}
	svc.SetAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure, "Secure should be set when the request itself used TLS")
}

func TestSetAuthCookie_XForwardedProtoFromUntrustedClientIgnored(t *testing.T) {
	// With no trusted-proxy checker, X-Forwarded-Proto must be ignored —
	// otherwise any client could spoof "https" and trick us into setting
	// the Secure flag on a cookie that travels in cleartext.
	svc := newServiceOrFail(t, "pass", nil)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	r.RemoteAddr = "203.0.113.50:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	svc.SetAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.False(t, cookies[0].Secure, "Secure must not be set based on a spoofable header")
}

func TestSetAuthCookie_XForwardedProtoFromTrustedProxyHonored(t *testing.T) {
	svc := newServiceOrFail(t, "pass", func(_ *http.Request) bool { return true })

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	svc.SetAuthCookie(w, r, "test-token", JWTTokenDuration)

	cookies := w.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.True(t, cookies[0].Secure, "Secure should be set when XFP=https is forwarded by a trusted proxy")
}

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

func TestService_CreateLaunchTicket(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)

	ticket, err := svc.CreateLaunchTicket()
	require.NoError(t, err)
	assert.Len(t, ticket, 43)

	// Ticket should be consumable through the public API.
	assert.True(t, svc.ConsumeLaunchTicket(ticket))
	assert.False(t, svc.ConsumeLaunchTicket(ticket))
}
