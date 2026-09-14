// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
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

func TestLogin_Success(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	token, err := svc.Login(nonce, computeChallenge("secret", nonce))
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	_, err = svc.Login(nonce, computeChallenge("wrong-password", nonce))
	assert.ErrorIs(t, err, ErrInvalidPassword)
}

func TestLogin_InvalidNonce(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	_, err := svc.Login("deadbeef", "anything")
	assert.ErrorIs(t, err, ErrInvalidNonce)
}

func TestLogin_NonceReplay(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)
	response := computeChallenge("secret", nonce)

	// First attempt should succeed
	_, err = svc.Login(nonce, response)
	require.NoError(t, err)

	// Replay the same nonce — should fail
	_, err = svc.Login(nonce, response)
	assert.ErrorIs(t, err, ErrInvalidNonce)
}

func TestLogin_JWTContainsExpAndIat(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	token, err := svc.Login(nonce, computeChallenge("secret", nonce))
	require.NoError(t, err)

	tok, err := svc.jwtAuth.Decode(token)
	require.NoError(t, err)
	exp, ok := tok.Expiration()
	assert.True(t, ok, "token should have expiration")
	assert.False(t, exp.IsZero())
	iat, ok := tok.IssuedAt()
	assert.True(t, ok, "token should have issued-at")
	assert.False(t, iat.IsZero())
}

func TestBuildAuthCookie(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	c := svc.BuildAuthCookie("test-token", JWTTokenDuration, false)

	assert.Equal(t, CookieName, c.Name)
	assert.Equal(t, "test-token", c.Value)
	assert.Equal(t, CookiePath, c.Path)
	assert.True(t, c.HttpOnly)
	assert.False(t, c.Secure)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, int(JWTTokenDuration.Seconds()), c.MaxAge)
}

func TestBuildAuthCookie_Secure(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	c := svc.BuildAuthCookie("test-token", JWTTokenDuration, true)
	assert.True(t, c.Secure)
}

func TestIsSecureRequest_DirectTLS(t *testing.T) {
	// A request delivered over TLS (r.TLS != nil) is secure with no proxy
	// involved — this is the free win once the daemon serves HTTPS directly.
	svc := newServiceOrFail(t, "pass", nil)
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.TLS = &tls.ConnectionState{}
	assert.True(t, svc.IsSecureRequest(r))
}

func TestIsSecureRequest_XForwardedProtoFromUntrustedClientIgnored(t *testing.T) {
	// With no trusted-proxy checker, X-Forwarded-Proto must be ignored —
	// otherwise any client could spoof "https" and trick us into setting
	// the Secure flag on a cookie that travels in cleartext.
	svc := newServiceOrFail(t, "pass", nil)
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.50:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.False(t, svc.IsSecureRequest(r), "must not be secure based on a spoofable header")
}

func TestIsSecureRequest_XForwardedProtoFromTrustedProxyHonored(t *testing.T) {
	svc := newServiceOrFail(t, "pass", func(_ *http.Request) bool { return true })

	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	assert.True(t, svc.IsSecureRequest(r), "should be secure when XFP=https is forwarded by a trusted proxy")
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
