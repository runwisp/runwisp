// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/sebest/xff"
	"log/slog"

	srp "mz.attahri.com/code/srp/v3"
)

const (
	MaxAuthAttempts    = 5
	AuthRateWindow     = 5 * time.Minute
	JWTTokenDuration   = 24 * time.Hour
	JWTIssuer          = "runwisp"
	JWTAudience        = "runwisp-api"
	MaxRequestBodySize = 8 * 1024 // 8 KB — SRP exchanges carry hex-encoded big ints
	authCookieName     = "runwisp_jwt"
	authCookiePath     = "/api/"
	srpSessionTTL      = 60 * time.Second
	srpSessionCapacity = 1000
	launchTicketTTL    = 60 * time.Second
)

// srpSession holds the per-login server state between /srp/start and
// /srp/finish. Single-use — popped from the store as soon as /finish
// consumes it (whether the proof verifies or not).
type srpSession struct {
	server *srp.Server
}

// srpSessionStore is a TTL- and capacity-bounded map of in-flight SRP
// handshakes. Bounding the capacity prevents a stranger from
// memory-exhausting the daemon with bogus /srp/start calls; the
// per-IP rate limit on the route is the primary defense.
type srpSessionStore struct {
	entries *expirable.LRU[string, *srpSession]
}

func newSRPSessionStore() *srpSessionStore {
	return &srpSessionStore{
		entries: expirable.NewLRU[string, *srpSession](srpSessionCapacity, nil, srpSessionTTL),
	}
}

func (s *srpSessionStore) put(id string, session *srpSession) {
	s.entries.Add(id, session)
}

// pop atomically removes and returns a session by id. Single-use: a
// second pop for the same id always returns nil. Combined with
// expirable.LRU's TTL, this rules out replay both within and after the
// session window.
func (s *srpSessionStore) pop(id string) *srpSession {
	v, ok := s.entries.Get(id)
	if !ok {
		return nil
	}
	s.entries.Remove(id)
	return v
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

// AuthService handles SRP authentication and JWT token issuance.
type AuthService struct {
	jwtAuth        *jwtauth.JWTAuth
	verifier       []byte
	salt           []byte
	srpSessions    *srpSessionStore
	launchTickets  *launchTicketStore
	trustedProxies *xff.Options
}

// NewAuthService creates an AuthService with the given SRP credentials and
// JWT signing secret. trustedProxies is consulted when deciding whether to
// honor X-Forwarded-Proto on cookie issuance; pass nil if the daemon sits
// directly on the network.
func NewAuthService(verifier, salt []byte, jwtSecret string, trustedProxies *xff.Options) (*AuthService, error) {
	if jwtSecret == "" {
		return nil, errors.New("jwtSecret must not be empty; resolve it from the database before creating AuthService")
	}
	if len(verifier) == 0 || len(salt) == 0 {
		return nil, errors.New("SRP verifier and salt must be provided")
	}

	return &AuthService{
		jwtAuth: jwtauth.New(
			"HS256", []byte(jwtSecret), nil,
			jwt.WithIssuer(JWTIssuer),
			jwt.WithAudience(JWTAudience),
		),
		verifier:       verifier,
		salt:           salt,
		srpSessions:    newSRPSessionStore(),
		launchTickets:  newLaunchTicketStore(),
		trustedProxies: trustedProxies,
	}, nil
}

// JWTAuth returns the underlying jwtauth instance for use in middleware.
func (a *AuthService) JWTAuth() *jwtauth.JWTAuth {
	return a.jwtAuth
}

// isSecureRequest reports whether the connection delivering r used TLS. We
// trust X-Forwarded-Proto only when the immediate peer is in the operator's
// configured trusted-proxy set; otherwise any client could falsely claim TLS
// and trick us into setting Secure on a cookie that travels in cleartext.
func (a *AuthService) isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if isFromTrustedProxy(r, a.trustedProxies) {
		return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	}
	return false
}

func (a *AuthService) setAuthCookie(w http.ResponseWriter, r *http.Request, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     authCookiePath,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   a.isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// IssueJWT mints a JWT with the standard daemon claims (iss, aud, iat, exp).
// Used both by the SRP finish flow and by the local-JWT shortcut path.
func (a *AuthService) IssueJWT(ttl time.Duration, extra map[string]any) (string, error) {
	claims := map[string]any{
		"exp": time.Now().Add(ttl).Unix(),
		"iat": time.Now().Unix(),
		"iss": JWTIssuer,
		"aud": JWTAudience,
	}
	for k, v := range extra {
		claims[k] = v
	}
	_, ts, err := a.jwtAuth.Encode(claims)
	return ts, err
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

// handleSRPStart allocates a session and returns the SRP salt and server
// ephemeral B. The client cannot send A yet because its 'x' value (and
// therefore its proofs M1/M2) depends on the salt the server holds — A is
// independent of salt but binding the rest of the handshake to a specific
// A requires the salt first. So we hand the salt over in the response and
// receive A together with M1 in /srp/finish.
func (a *AuthService) handleSRPStart(resp http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(resp, req.Body, MaxRequestBodySize)

	server, err := srp.NewServer(datadir.SRPParams(), datadir.SRPIdentity, a.salt, a.verifier)
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to start SRP session", err)
		return
	}

	sessionID, err := datadir.RandBase62(43)
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to allocate session id", err)
		return
	}
	a.srpSessions.put(sessionID, &srpSession{server: server})

	respondJSON(resp, http.StatusOK, SRPStartResponseBody{
		SessionID: sessionID,
		Salt:      hex.EncodeToString(a.salt),
		B:         hex.EncodeToString(server.B()),
	})
}

// handleSRPFinish consumes the session, sets the client's ephemeral A,
// verifies the client proof, issues the JWT, and returns the server proof
// so the client can authenticate the daemon back. Failures destroy the
// session — no second attempt against the same session id.
func (a *AuthService) handleSRPFinish(resp http.ResponseWriter, req *http.Request) {
	req.Body = http.MaxBytesReader(resp, req.Body, MaxRequestBodySize)
	var body struct {
		SessionID string `json:"sessionID"`
		A         string `json:"A"`
		M1        string `json:"M1"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondBadRequest(resp, "Invalid request")
		return
	}

	session := a.srpSessions.pop(body.SessionID)
	if session == nil {
		respondUnauthorized(resp, "Unknown or expired session")
		return
	}
	A, err := hex.DecodeString(body.A)
	if err != nil || len(A) == 0 {
		respondBadRequest(resp, "Invalid A")
		return
	}
	if err := session.server.SetA(A); err != nil {
		// SetA validates A; an invalid public ephemeral is a client error.
		respondBadRequest(resp, "Invalid A")
		return
	}
	M1, err := hex.DecodeString(body.M1)
	if err != nil {
		respondUnauthorized(resp, "Invalid M1")
		return
	}
	ok, err := session.server.CheckM1(M1)
	if err != nil || !ok {
		respondUnauthorized(resp, "Invalid password")
		return
	}
	M2, err := session.server.ComputeM2()
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to compute server proof", err)
		return
	}

	tokenString, err := a.IssueJWT(JWTTokenDuration, nil)
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to generate token", err)
		return
	}

	a.setAuthCookie(resp, req, tokenString, JWTTokenDuration)
	respondJSON(resp, http.StatusOK, SRPFinishResponseBody{
		M2:    hex.EncodeToString(M2),
		Token: tokenString,
	})
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
//
// An optional `redirect` query parameter targets a same-origin path (must
// start with `/` and not `//`) — used by the TUI's "download log" action so
// the operator's browser lands directly on the raw-log endpoint with a fresh
// session cookie. Cross-origin or scheme-relative redirects are rejected to
// avoid an open-redirect surface.
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

	tokenString, err := srv.auth.IssueJWT(JWTTokenDuration, nil)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token", err)
		return
	}

	srv.auth.setAuthCookie(w, r, tokenString, JWTTokenDuration)
	http.Redirect(w, r, sanitizeLaunchRedirect(r.URL.Query().Get("redirect")), http.StatusSeeOther)
}

// sanitizeLaunchRedirect returns the requested redirect target if it is a
// safe same-origin absolute path; otherwise it falls back to "/". A safe
// target starts with a single "/" (so "/api/..." is allowed) but not "//"
// (which would be scheme-relative and could escape the origin).
func sanitizeLaunchRedirect(target string) string {
	if target == "" || len(target) < 1 || target[0] != '/' {
		return "/"
	}
	if len(target) >= 2 && target[1] == '/' {
		return "/"
	}
	return target
}

// formatSRPError centralises the slog of upstream srp library failures so
// caller code stays terse. Currently unused but reserved for richer error
// reporting before 1.0.
func formatSRPError(stage string, err error) string {
	if err == nil {
		return ""
	}
	slog.Debug("SRP stage failed", "stage", stage, "err", err)
	return fmt.Sprintf("%s failed", stage)
}
