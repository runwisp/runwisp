// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth implements the daemon's CHAP login + session-token flow. It
// owns the Service struct, its single-use stores (nonces, launch
// tickets), and the cookie/secure-flag logic. Transport concerns
// (local-trusted gating, chi route registration, rate-limiting) stay in
// the parent server package so this package has no HTTP-router or
// peer-credential coupling.
package auth

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/chap"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
)

const (
	// MaxAuthAttempts and AuthRateWindow bound login attempts per IP. The
	// challenge and login share the same limiter so a hostile client can't
	// flood the nonce store while spreading login probes under the limit.
	MaxAuthAttempts = 5
	AuthRateWindow  = 5 * time.Minute

	SessionDuration = 24 * time.Hour

	// MaxRequestBodySize caps the auth login request body. The handler only
	// reads a tiny JSON object; anything larger is hostile.
	MaxRequestBodySize = 1024 // 1 KB

	// CookieName is the session cookie name. Path is scoped so the cookie
	// only rides on API requests, not on UI asset fetches.
	CookieName = "runwisp_jwt"
	CookiePath = "/api/"

	nonceTTL        = 5 * time.Minute
	launchTicketTTL = 60 * time.Second

	// sessionKDFInfo namespaces the session-key derivation (folded into the
	// PBKDF2 salt). Bumping it is the way to invalidate every existing session
	// on the next restart without changing the operator's RUNWISP_PASSWORD.
	sessionKDFInfo = "runwisp-session-v1"
)

// ErrEmptySessionKey is returned by NewService when the supplied session key
// is empty. The caller derives it with DeriveSessionKey before constructing
// the service.
var ErrEmptySessionKey = errors.New("auth: sessionKey must not be empty")

// DeriveSessionKey produces the session-token HMAC key from the daemon
// password, salted by the per-install fingerprint. Properties:
//
//   - Stable across restarts when both inputs are stable, so a browser
//     session backed by RUNWISP_PASSWORD survives a daemon restart.
//   - Rotates automatically when the password rotates — including the
//     ephemeral-password case, where each boot mints a new password and
//     thus invalidates any prior session.
//   - Different per machine/cwd thanks to the fingerprint salt; the same
//     password on another host does not yield the same key.
//
// It uses the same expensive KDF (PBKDF2-HMAC-SHA256 at chap.Iterations) as
// the CHAP login. The session token travels in the same channel as the CHAP
// transcript (cleartext on TLS-less deployments) and the fingerprint salt is
// built from non-secret inputs, so a cheaper KDF would give an eavesdropper
// holding any token a fast offline oracle for a weak RUNWISP_PASSWORD,
// bypassing the CHAP iteration cost.
func DeriveSessionKey(password, fingerprint string) ([]byte, error) {
	salt := []byte(sessionKDFInfo + "\x00" + fingerprint)
	key, err := pbkdf2.Key(sha256.New, password, salt, chap.Iterations, 32)
	if err != nil {
		return nil, fmt.Errorf("derive session key: %w", err)
	}
	return key, nil
}

// TrustedProxyChecker reports whether the immediate TCP peer of r is in
// the operator's trusted-proxy set. Injected from the parent server
// package so this package never reaches into request context for keys it
// doesn't own. Pass nil for daemons that sit directly on the network.
type TrustedProxyChecker func(*http.Request) bool

// Service handles challenge-response authentication and session-token
// issuance. It is safe for concurrent use.
type Service struct {
	sessionKey     []byte
	password       string
	nonces         *ttlStore
	launchTickets  *ttlStore
	trustedProxies TrustedProxyChecker
}

// NewService builds a Service. sessionKey must be non-empty; callers derive
// it with DeriveSessionKey. trustedProxies is consulted when deciding whether to honor
// X-Forwarded-Proto on cookie issuance; pass nil for direct-internet
// deployments.
func NewService(password string, sessionKey []byte, trustedProxies TrustedProxyChecker) (*Service, error) {
	if len(sessionKey) == 0 {
		return nil, ErrEmptySessionKey
	}
	return &Service{
		sessionKey:     sessionKey,
		password:       password,
		nonces:         newNonceStore(),
		launchTickets:  newLaunchTicketStore(),
		trustedProxies: trustedProxies,
	}, nil
}

// Password returns the in-memory daemon password. Disclosure is gated by
// the local-credentials endpoint in the server package; this accessor
// exists so that gate can return the value to the local CLI/TUI.
func (s *Service) Password() string { return s.password }

// IssueChallenge mints a single-use nonce; the client signs it with the
// shared password and returns chap.Response (PBKDF2-HMAC-SHA256 of the
// password salted with the nonce).
func (s *Service) IssueChallenge() (string, error) {
	return s.nonces.create()
}

// CreateLaunchTicket mints a single-use, short-lived ticket the TUI can
// hand to the browser to bootstrap a session cookie without exposing the
// password in a URL.
func (s *Service) CreateLaunchTicket() (string, error) {
	return s.launchTickets.create()
}

// ConsumeLaunchTicket validates and consumes a launch ticket. Returns
// false if the ticket is unknown or expired.
func (s *Service) ConsumeLaunchTicket(ticket string) bool {
	return s.launchTickets.consume(ticket)
}

// IssueToken mints a session token that expires ttl from now. The token is
// "<exp>.<mac>": exp is the expiry in unix seconds, left readable so clients
// can drop a cached token before it goes stale (see TokenExpiry), and mac is
// HMAC-SHA256 of exp under the session key, so only this daemon can mint or
// extend one. The key is derived from the daemon password, so rotating the
// password revokes every outstanding token.
func (s *Service) IssueToken(ttl time.Duration) string {
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	return exp + "." + s.sign(exp)
}

func (s *Service) sign(exp string) string {
	mac := hmac.New(sha256.New, s.sessionKey)
	mac.Write([]byte(exp))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// ValidToken reports whether token was minted by this daemon and has not
// expired. An empty token is invalid.
func (s *Service) ValidToken(token string) bool {
	expiry, ok := TokenExpiry(token)
	exp, mac, _ := strings.Cut(token, ".")
	return ok && time.Now().Before(expiry) && hmac.Equal([]byte(mac), []byte(s.sign(exp)))
}

// TokenExpiry reads the expiry out of a session token without checking its
// MAC. Only the daemon can tell whether a token is genuine; clients use this
// to stop reusing a cached token the daemon is about to reject.
func TokenExpiry(token string) (time.Time, bool) {
	exp, _, ok := strings.Cut(token, ".")
	unix, err := strconv.ParseInt(exp, 10, 64)
	return time.Unix(unix, 0), ok && err == nil
}

// BuildAuthCookie builds the session cookie value. secure is decided by the
// caller (see IsSecureRequest) rather than taken as a *http.Request here, so
// it can be called from a huma handler that only has a context.Context.
func (s *Service) BuildAuthCookie(token string, ttl time.Duration, secure bool) http.Cookie {
	return http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     CookiePath,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   secure, // NOSONAR: intentionally dynamic — true when direct TLS or trusted-proxy TLS, false for plain HTTP dev setups
		SameSite: http.SameSiteStrictMode,
	}
}

// IsSecureRequest reports whether the connection delivering r used TLS.
// X-Forwarded-Proto is only honored when the immediate peer is in the
// configured trusted-proxy set — otherwise any client could falsely claim
// TLS and trick us into setting Secure on a cookie that travels in
// cleartext.
func (s *Service) IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.trustedProxies != nil && s.trustedProxies(r) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

// ErrInvalidNonce is returned by Login when the nonce is unknown, expired, or
// already consumed.
var ErrInvalidNonce = errors.New("invalid or expired challenge")

// ErrInvalidPassword is returned by Login when response does not match the
// expected CHAP proof for nonce.
var ErrInvalidPassword = errors.New("invalid password")

// Login is the CHAP login flow: it consumes the single-use nonce, verifies
// the signed response, and issues a fresh session token on success. It has no
// HTTP-layer concerns (cookies, status codes) so the caller — a huma handler
// in the parent server package — can decide how to deliver the token.
func (s *Service) Login(nonce, response string) (token string, err error) {
	if !s.nonces.consume(nonce) {
		return "", ErrInvalidNonce
	}

	expected := chap.Response(s.password, nonce)
	if subtle.ConstantTimeCompare([]byte(response), []byte(expected)) != 1 {
		return "", ErrInvalidPassword
	}

	return s.IssueToken(SessionDuration), nil
}

// BearerToken extracts the credential from an Authorization header value of
// the form "Bearer <token>" (scheme case-insensitive). It returns "" for any
// other scheme or an empty token.
func BearerToken(authorization string) string {
	scheme, token, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// TokenFromRequest returns the caller's session token: a Bearer
// Authorization header wins, else the session cookie. Any other
// Authorization scheme (e.g. ambient proxy Basic auth) is ignored. Returns
// "" when neither carries a token.
func TokenFromRequest(r *http.Request) string {
	if token := BearerToken(r.Header.Get("Authorization")); token != "" {
		return token
	}
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ttlStore holds single-use tokens with TTL expiry, minted by gen. Both the
// challenge nonce and the browser launch ticket are "generate a random token,
// redeem it once before it expires" — they differ only in size, TTL, and how
// the token is generated. A full store drops expired tokens, then the oldest,
// so a flood of challenge requests can't grow it without bound.
type ttlStore struct {
	// mu makes consume's lookup-then-delete atomic: two requests presenting the
	// same token must not both redeem it. This is the login boundary, so
	// single-use must actually be single.
	mu      sync.Mutex
	entries map[string]time.Time // token → expiry
	size    int
	ttl     time.Duration
	gen     func() (string, error)
}

func newTTLStore(size int, ttl time.Duration, gen func() (string, error)) *ttlStore {
	return &ttlStore{entries: make(map[string]time.Time, size), size: size, ttl: ttl, gen: gen}
}

func (s *ttlStore) create() (string, error) {
	token, err := s.gen()
	if err != nil {
		return "", err
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) >= s.size {
		s.evict(now)
	}
	s.entries[token] = now.Add(s.ttl)
	return token, nil
}

// evict drops every expired token; if none had expired, it drops the oldest.
func (s *ttlStore) evict(now time.Time) {
	var oldest string
	var oldestExp time.Time
	for token, exp := range s.entries {
		if now.After(exp) {
			delete(s.entries, token)
		} else if oldest == "" || exp.Before(oldestExp) {
			oldest, oldestExp = token, exp
		}
	}
	if len(s.entries) >= s.size {
		delete(s.entries, oldest)
	}
}

func (s *ttlStore) consume(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.entries[token]
	delete(s.entries, token)
	return ok && !time.Now().After(exp)
}

// newNonceStore holds single-use challenge nonces.
func newNonceStore() *ttlStore {
	return newTTLStore(1000, nonceTTL, func() (string, error) {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return hex.EncodeToString(buf), nil
	})
}

// newLaunchTicketStore holds single-use launch tickets. A launch ticket
// allows one-click browser authentication from the TUI without exposing the
// password in the URL. 43 base62 chars ≈ 256 bits of entropy (matches the
// prior 32-byte hex ticket) while shrinking the URL-visible token from 64 to
// 43 characters.
func newLaunchTicketStore() *ttlStore {
	return newTTLStore(100, launchTicketTTL, func() (string, error) {
		return datadir.RandBase62(43)
	})
}
