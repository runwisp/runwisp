// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/runwisp/runwisp/internal/datadir"
	"log/slog"
)

const (
	MaxAuthAttempts    = 5
	AuthRateWindow     = 5 * time.Minute
	JWTTokenDuration   = 24 * time.Hour
	JWTIssuer          = "runwisp"
	JWTAudience        = "runwisp-api"
	MaxRequestBodySize = 1024 // 1 KB for auth requests
	authCookieName     = "runwisp_jwt"
	authCookiePath     = "/api/"
	nonceTTL           = 5 * time.Minute
	launchTicketTTL    = 60 * time.Second
)

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

// launchTicketStore holds single-use launch tickets with TTL expiry.
// A launch ticket allows one-click browser authentication from the TUI
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
	// 43 base62 chars ≈ 256 bits of entropy (matches the prior 32-byte hex ticket)
	// while shrinking the URL-visible token from 64 to 43 characters.
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

// AuthService handles challenge-response authentication and JWT token issuance.
type AuthService struct {
	jwtAuth       *jwtauth.JWTAuth
	password      string
	nonces        *nonceStore
	launchTickets *launchTicketStore
}

// NewAuthService creates an AuthService with the given password and JWT signing secret.
func NewAuthService(password, jwtSecret string) *AuthService {
	if jwtSecret == "" {
		panic("jwtSecret must not be empty; it should be resolved from the database before creating AuthService")
	}

	return &AuthService{
		jwtAuth:       jwtauth.New("HS256", []byte(jwtSecret), nil),
		password:      password,
		nonces:        newNonceStore(),
		launchTickets: newLaunchTicketStore(),
	}
}

// JWTAuth returns the underlying jwtauth instance for use in middleware.
func (a *AuthService) JWTAuth() *jwtauth.JWTAuth {
	return a.jwtAuth
}

func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return r.Header.Get("X-Forwarded-Proto") == "https"
}

func setAuthCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     authCookiePath,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// registerAuthRoutes registers public auth endpoints as huma operations.
func (srv *Server) registerAuthRoutes() {
	// authStatus is a raw chi handler so it can inspect the request cookie.
	srv.router.Get("/api/auth/status", srv.handleAuthStatus)
}

func (srv *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authenticated := false

	if cookieToken := tokenFromCookie(authCookieName)(r); cookieToken != "" {
		if tok, err := srv.auth.jwtAuth.Decode(cookieToken); err == nil {
			if exp, ok := tok.Expiration(); ok && time.Now().Before(exp) {
				authenticated = true
			}
		}
	}

	respondJSON(w, http.StatusOK, AuthStatusBody{
		AuthRequired:  true,
		Authenticated: authenticated,
	})
}

// handleAuthChallenge is a raw chi handler (not huma) so it can sit under the
// rate-limiter that also protects the auth POST, preventing nonce-store flooding.
func (srv *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, err := srv.auth.nonces.create()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate challenge", err)
		return
	}
	respondJSON(w, http.StatusOK, AuthChallengeBody{Nonce: nonce})
}

// handleAuth stays as a raw chi handler because it needs httprate middleware
// and sets cookies directly on the response writer.
func (a *AuthService) handleAuth(resp http.ResponseWriter, req *http.Request) {
	var reqBody struct {
		Nonce    string `json:"nonce"`
		Response string `json:"response"`
	}

	req.Body = http.MaxBytesReader(resp, req.Body, MaxRequestBodySize)
	if err := json.NewDecoder(req.Body).Decode(&reqBody); err != nil {
		slog.Warn("Failed to decode auth request", "err", err)
		respondBadRequest(resp, "Invalid request")
		return
	}

	if !a.nonces.consume(reqBody.Nonce) {
		respondUnauthorized(resp, "Invalid or expired challenge")
		return
	}

	h := sha256.Sum256([]byte(a.password + ":" + reqBody.Nonce))
	expected := hex.EncodeToString(h[:])

	if subtle.ConstantTimeCompare([]byte(reqBody.Response), []byte(expected)) != 1 {
		respondUnauthorized(resp, "Invalid password")
		return
	}

	_, tokenString, err := a.jwtAuth.Encode(map[string]any{
		"exp": time.Now().Add(JWTTokenDuration).Unix(),
		"iat": time.Now().Unix(),
		"iss": JWTIssuer,
		"aud": JWTAudience,
	})

	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to generate token", err)
		return
	}

	setAuthCookie(resp, req, tokenString, JWTTokenDuration)
	resp.Header().Set("Content-Type", "application/json")
	resp.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(resp).Encode(map[string]string{"token": tokenString}); err != nil {
		slog.Error("Failed to encode auth response", "err", err)
	}
}

// CreateLaunchTicket generates a single-use, short-lived launch ticket.
func (a *AuthService) CreateLaunchTicket() (string, error) {
	return a.launchTickets.create()
}

// handleCreateLaunchTicket generates a launch ticket via the authenticated API.
// Restricted to loopback so that only local TUI/CLI clients can request tickets.
func (srv *Server) handleCreateLaunchTicket(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		respondForbidden(w, "Launch tickets can only be created from localhost")
		return
	}

	ticket, err := srv.auth.CreateLaunchTicket()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create launch ticket", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"ticket": ticket})
}

// isLoopbackRequest returns true if the request originates from a loopback address.
// Uses the original TCP peer address stored by savePeerAddr middleware to prevent
// bypass via spoofed X-Real-IP / X-Forwarded-For headers (middleware.RealIP
// overwrites r.RemoteAddr with those values).
func isLoopbackRequest(r *http.Request) bool {
	addr := r.RemoteAddr
	if peerAddr, ok := r.Context().Value(peerAddrContextKey).(string); ok {
		addr = peerAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// handleLaunchTicket redeems a single-use launch ticket and redirects to the
// UI with a session cookie. Only accepts requests from loopback addresses.
func (srv *Server) handleLaunchTicket(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		respondForbidden(w, "Launch tickets are only valid from localhost")
		return
	}

	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		respondBadRequest(w, "Missing ticket parameter")
		return
	}

	if !srv.auth.launchTickets.consume(ticket) {
		respondUnauthorized(w, "Invalid or expired launch ticket")
		return
	}

	_, tokenString, err := srv.auth.jwtAuth.Encode(map[string]any{
		"exp": time.Now().Add(JWTTokenDuration).Unix(),
		"iat": time.Now().Unix(),
		"iss": JWTIssuer,
		"aud": JWTAudience,
	})
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token", err)
		return
	}

	setAuthCookie(w, r, tokenString, JWTTokenDuration)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
