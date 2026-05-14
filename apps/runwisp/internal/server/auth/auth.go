// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package auth implements the daemon's CHAP login + JWT issuance flow. It
// owns the Service struct, its single-use stores (nonces, launch
// tickets), and the cookie/secure-flag logic. Transport concerns
// (local-trusted gating, chi route registration, rate-limiting) stay in
// the parent server package so this package has no HTTP-router or
// peer-credential coupling.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/runwisp/runwisp/internal/datadir"
	"log/slog"
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
	nonces         *nonceStore
	launchTickets  *launchTicketStore
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
// shared password and returns the SHA-256 over (password + ":" + nonce).
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

// SetAuthCookie writes the session cookie. The Secure flag follows the
// trusted-proxy policy in IsSecureRequest so a non-TLS daemon behind a
// trusted reverse proxy still gets a Secure cookie, while a hostile
// untrusted client cannot spoof one.
func (s *Service) SetAuthCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     CookiePath,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   s.IsSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
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

// HandleLogin is the CHAP login endpoint. It validates the nonce + signed
// response, issues a JWT, and sets the session cookie. It is a raw chi
// handler (not a huma operation) because it needs to set cookies directly
// and sits behind the httprate limiter wired by the parent package.
func (s *Service) HandleLogin(w http.ResponseWriter, r *http.Request) {
	var reqBody struct {
		Nonce    string `json:"nonce"`
		Response string `json:"response"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodySize)
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		slog.Warn("Failed to decode auth request", "err", err)
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if !s.nonces.consume(reqBody.Nonce) {
		http.Error(w, "Invalid or expired challenge", http.StatusUnauthorized)
		return
	}

	h := sha256.Sum256([]byte(s.password + ":" + reqBody.Nonce))
	expected := hex.EncodeToString(h[:])
	if subtle.ConstantTimeCompare([]byte(reqBody.Response), []byte(expected)) != 1 {
		http.Error(w, "Invalid password", http.StatusUnauthorized)
		return
	}

	token, err := s.IssueToken(JWTTokenDuration)
	if err != nil {
		slog.Error("Failed to generate token", "err", err)
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	s.SetAuthCookie(w, r, token, JWTTokenDuration)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"token": token}); err != nil {
		slog.Error("Failed to encode auth response", "err", err)
	}
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

// nonceStore holds single-use challenge nonces with TTL expiry.
type nonceStore struct {
	entries *expirable.LRU[string, time.Time]
}

func newNonceStore() *nonceStore {
	return &nonceStore{
		entries: expirable.NewLRU[string, time.Time](1000, nil, nonceTTL),
	}
}

func (s *nonceStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	nonce := hex.EncodeToString(buf)
	s.entries.Add(nonce, time.Now().Add(nonceTTL))
	return nonce, nil
}

func (s *nonceStore) consume(nonce string) bool {
	exp, ok := s.entries.Get(nonce)
	if !ok || time.Now().After(exp) {
		return false
	}
	s.entries.Remove(nonce)
	return true
}

// launchTicketStore holds single-use launch tickets with TTL expiry. A
// launch ticket allows one-click browser authentication from the TUI
// without exposing the password in the URL.
type launchTicketStore struct {
	entries *expirable.LRU[string, time.Time]
}

func newLaunchTicketStore() *launchTicketStore {
	return &launchTicketStore{
		entries: expirable.NewLRU[string, time.Time](100, nil, launchTicketTTL),
	}
}

func (s *launchTicketStore) create() (string, error) {
	// 43 base62 chars ≈ 256 bits of entropy (matches the prior 32-byte hex
	// ticket) while shrinking the URL-visible token from 64 to 43 characters.
	ticket, err := datadir.RandBase62(43)
	if err != nil {
		return "", err
	}
	s.entries.Add(ticket, time.Now().Add(launchTicketTTL))
	return ticket, nil
}

func (s *launchTicketStore) consume(ticket string) bool {
	exp, ok := s.entries.Get(ticket)
	if !ok || time.Now().After(exp) {
		return false
	}
	s.entries.Remove(ticket)
	return true
}
