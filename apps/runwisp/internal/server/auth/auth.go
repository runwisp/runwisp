// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package auth implements the daemon's CHAP login + JWT issuance flow. It
// owns the Service struct, its single-use stores (nonces, launch
// tickets), and the cookie/secure-flag logic. Transport concerns
// (local-trusted gating, chi route registration, rate-limiting) stay in
// the parent server package so this package has no HTTP-router or
// peer-credential coupling.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/runwisp/runwisp/internal/chap"
	"github.com/runwisp/runwisp/internal/datadir"
)

const (
	// MaxAuthAttempts and AuthRateWindow bound login attempts per IP. The
	// challenge and login share the same limiter so a hostile client can't
	// flood the nonce store while spreading login probes under the limit.
	MaxAuthAttempts = 5
	AuthRateWindow  = 5 * time.Minute

	JWTTokenDuration = 24 * time.Hour
	JWTIssuer        = "runwisp"
	JWTAudience      = "runwisp-api"

	// MaxRequestBodySize caps the auth login request body. The handler only
	// reads a tiny JSON object; anything larger is hostile.
	MaxRequestBodySize = 1024 // 1 KB

	// CookieName is the session cookie name. Path is scoped so the cookie
	// only rides on API requests, not on UI asset fetches.
	CookieName = "runwisp_jwt"
	CookiePath = "/api/"

	nonceTTL        = 5 * time.Minute
	launchTicketTTL = 60 * time.Second
)

// ErrEmptyJWTSecret is returned by NewService when the supplied JWT secret
// is empty. The caller is responsible for resolving the secret (typically
// from internal/datadir) before constructing the service.
var ErrEmptyJWTSecret = errors.New("auth: jwtSecret must not be empty")

// TrustedProxyChecker reports whether the immediate TCP peer of r is in
// the operator's trusted-proxy set. Injected from the parent server
// package so this package never reaches into request context for keys it
// doesn't own. Pass nil for daemons that sit directly on the network.
type TrustedProxyChecker func(*http.Request) bool

// Service handles challenge-response authentication and JWT token issuance.
// It is safe for concurrent use.
type Service struct {
	jwtAuth        *jwtauth.JWTAuth
	password       string
	nonces         *ttlStore
	launchTickets  *ttlStore
	trustedProxies TrustedProxyChecker
}

// NewService builds a Service. jwtSecret must be non-empty; callers resolve
// it from the data dir (see internal/datadir) before reaching this point.
// trustedProxies is consulted when deciding whether to honor
// X-Forwarded-Proto on cookie issuance; pass nil for direct-internet
// deployments.
func NewService(password, jwtSecret string, trustedProxies TrustedProxyChecker) (*Service, error) {
	if jwtSecret == "" {
		return nil, ErrEmptyJWTSecret
	}
	return &Service{
		jwtAuth: jwtauth.New(
			"HS256", []byte(jwtSecret), nil,
			jwt.WithIssuer(JWTIssuer),
			jwt.WithAudience(JWTAudience),
		),
		password:       password,
		nonces:         newNonceStore(),
		launchTickets:  newLaunchTicketStore(),
		trustedProxies: trustedProxies,
	}, nil
}

// JWTAuth returns the underlying jwtauth instance for use in middleware.
func (s *Service) JWTAuth() *jwtauth.JWTAuth { return s.jwtAuth }

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

// IssueToken signs a fresh JWT with the standard runwisp claims. ttl
// controls the exp claim relative to now.
func (s *Service) IssueToken(ttl time.Duration) (string, error) {
	now := time.Now()
	_, tokenString, err := s.jwtAuth.Encode(map[string]any{
		"exp": now.Add(ttl).Unix(),
		"iat": now.Unix(),
		"iss": JWTIssuer,
		"aud": JWTAudience,
	})
	return tokenString, err
}

// DecodeCookieToken parses a JWT extracted from the session cookie and
// reports whether it is non-empty and unexpired. The boolean lets the
// status endpoint distinguish "no cookie" from "stale cookie" without
// callers learning JWT internals.
func (s *Service) DecodeCookieToken(token string) (valid bool) {
	if token == "" {
		return false
	}
	tok, err := s.jwtAuth.Decode(token)
	if err != nil {
		return false
	}
	exp, ok := tok.Expiration()
	if !ok {
		return false
	}
	return time.Now().Before(exp)
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
// the signed response, and issues a fresh JWT on success. It has no
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

	return s.IssueToken(JWTTokenDuration)
}

// TokenFromCookie reads the session cookie value off r, returning "" when
// the cookie is absent. Use this as a jwtauth.TokenSource.
func TokenFromCookie(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// ttlStore holds single-use tokens with TTL expiry, minted by gen. Both the
// challenge nonce and the browser launch ticket are "generate a random token,
// redeem it once before it expires" — they differ only in size, TTL, and how
// the token is generated.
type ttlStore struct {
	// mu makes consume's get-then-remove atomic: the LRU locks each call
	// individually, so without this two requests presenting the same token could
	// both observe it before either removed it, redeeming a single-use token
	// twice. This is the login boundary, so single-use must actually be single.
	mu      sync.Mutex
	entries *expirable.LRU[string, time.Time]
	ttl     time.Duration
	gen     func() (string, error)
}

func newTTLStore(size int, ttl time.Duration, gen func() (string, error)) *ttlStore {
	return &ttlStore{
		entries: expirable.NewLRU[string, time.Time](size, nil, ttl),
		ttl:     ttl,
		gen:     gen,
	}
}

func (s *ttlStore) create() (string, error) {
	token, err := s.gen()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.entries.Add(token, time.Now().Add(s.ttl))
	s.mu.Unlock()
	return token, nil
}

func (s *ttlStore) consume(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.entries.Get(token)
	if !ok || time.Now().After(exp) {
		return false
	}
	s.entries.Remove(token)
	return true
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
