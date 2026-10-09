// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/chap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewService_RejectsEmptySecret(t *testing.T) {
	svc, err := NewService("pass", nil, nil)
	assert.ErrorIs(t, err, ErrEmptySessionKey)
	assert.Nil(t, svc)
}

// DeriveSessionKey must be deterministic so browser sessions survive a daemon
// restart when RUNWISP_PASSWORD is stable, must change with the password so
// rotating it revokes every session, and must be salted per install so the
// same password on another host can't mint sessions here.
func TestDeriveSessionKey(t *testing.T) {
	derive := func(password, fingerprint string) []byte {
		key, err := DeriveSessionKey(password, fingerprint)
		require.NoError(t, err)
		return key
	}
	base := derive("secret", "alpha-fingerprint")
	assert.Len(t, base, 32)
	assert.Equal(t, base, derive("secret", "alpha-fingerprint"), "deterministic")
	assert.NotEqual(t, base, derive("other-secret", "alpha-fingerprint"), "rotates with the password")
	assert.NotEqual(t, base, derive("secret", "beta-fingerprint"), "salted per install")
}

// TestDeriveSessionKey_UsesExpensiveKDF pins the derivation to PBKDF2 at
// chap.Iterations. The fingerprint salt is built from non-secret inputs and a
// session token rides the same cleartext channel as the CHAP transcript on
// TLS-less deployments, so a captured token must not be a cheaper offline
// oracle for the password than the CHAP transcript is. A regression to a fast
// single-pass KDF would silently reopen that shortcut.
func TestDeriveSessionKey_UsesExpensiveKDF(t *testing.T) {
	got, err := DeriveSessionKey("secret", "alpha-fingerprint")
	require.NoError(t, err)
	want, err := pbkdf2.Key(sha256.New, "secret", []byte(sessionKDFInfo+"\x00alpha-fingerprint"), chap.Iterations, 32)
	require.NoError(t, err)
	assert.Equal(t, want, got)
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

func TestTTLStore_ExpiredTokenRejected(t *testing.T) {
	store := newTTLStore(10, -time.Second, func() (string, error) { return "tok", nil })
	token, err := store.create()
	require.NoError(t, err)
	assert.False(t, store.consume(token))
}

func TestTTLStore_FullStoreStaysBounded(t *testing.T) {
	n := 0
	store := newTTLStore(2, time.Minute, func() (string, error) {
		n++
		return fmt.Sprintf("tok-%d", n), nil
	})
	for range 3 {
		_, err := store.create()
		require.NoError(t, err)
	}
	// Bounded, and the newest token always survives. (Which older one goes is
	// not asserted: two tokens minted in the same clock tick tie.)
	assert.Len(t, store.entries, 2)
	assert.True(t, store.consume("tok-3"))
}

func computeChallenge(password, nonce string) string {
	return chap.Response(password, nonce)
}

func newServiceOrFail(t *testing.T, password string, trusted TrustedProxyChecker) *Service {
	t.Helper()
	svc, err := NewService(password, []byte("test-session-key"), trusted)
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

func TestLogin_IssuesValidSessionToken(t *testing.T) {
	svc := newServiceOrFail(t, "secret", nil)

	nonce, err := svc.nonces.create()
	require.NoError(t, err)

	token, err := svc.Login(nonce, computeChallenge("secret", nonce))
	require.NoError(t, err)

	assert.True(t, svc.ValidToken(token))
	exp, ok := TokenExpiry(token)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(SessionDuration), exp, 5*time.Second)
}

func TestValidToken(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	token := svc.IssueToken(time.Hour)
	exp, mac, _ := strings.Cut(token, ".")

	otherKey, err := NewService("pass", []byte("another-session-key"), nil)
	require.NoError(t, err)
	expired := svc.IssueToken(-time.Second)
	// Extending the expiry without re-signing must fail: the MAC covers exp.
	extended := "9999999999." + mac

	assert.True(t, svc.ValidToken(token))
	assert.False(t, svc.ValidToken(""), "empty")
	assert.False(t, svc.ValidToken(exp), "no MAC")
	assert.False(t, svc.ValidToken(extended), "tampered expiry")
	assert.False(t, svc.ValidToken(exp+".AAAA"), "wrong MAC")
	assert.False(t, svc.ValidToken(expired), "expired")
	assert.False(t, otherKey.ValidToken(token), "signed with a different key (password rotated)")
}

func TestTokenExpiry(t *testing.T) {
	exp, ok := TokenExpiry("1700000000.mac")
	require.True(t, ok)
	assert.Equal(t, int64(1700000000), exp.Unix())

	for _, bad := range []string{"", "1700000000", "soon.mac", "header.payload.sig"} {
		_, ok := TokenExpiry(bad)
		assert.False(t, ok, "%q must have no readable expiry", bad)
	}
}

func TestTokenFromRequest(t *testing.T) {
	withCookie := func(r *http.Request) *http.Request {
		r.AddCookie(&http.Cookie{Name: CookieName, Value: "from-cookie"})
		return r
	}
	tests := []struct {
		name          string
		authorization string
		cookie        bool
		want          string
	}{
		{"bearer header", "Bearer from-header", false, "from-header"},
		{"bearer scheme is case-insensitive", "bearer from-header", false, "from-header"},
		{"bearer wins over cookie", "Bearer from-header", true, "from-header"},
		{"cookie only", "", true, "from-cookie"},
		{"non-bearer scheme falls back to cookie", "Basic dXNlcjpwYXNz", true, "from-cookie"},
		{"nothing", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/tasks", nil)
			if tt.authorization != "" {
				r.Header.Set("Authorization", tt.authorization)
			}
			if tt.cookie {
				r = withCookie(r)
			}
			assert.Equal(t, tt.want, TokenFromRequest(r))
		})
	}
}

func TestBuildAuthCookie(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	c := svc.BuildAuthCookie("test-token", SessionDuration, false)

	assert.Equal(t, CookieName, c.Name)
	assert.Equal(t, "test-token", c.Value)
	assert.Equal(t, CookiePath, c.Path)
	assert.True(t, c.HttpOnly)
	assert.False(t, c.Secure)
	assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	assert.Equal(t, int(SessionDuration.Seconds()), c.MaxAge)
}

func TestBuildAuthCookie_Secure(t *testing.T) {
	svc := newServiceOrFail(t, "pass", nil)
	c := svc.BuildAuthCookie("test-token", SessionDuration, true)
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
