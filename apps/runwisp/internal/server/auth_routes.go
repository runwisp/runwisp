// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/runwisp/runwisp/internal/server/auth"
)

// This file is the transport adapter for the auth subsystem. The core CHAP /
// JWT flow lives in internal/server/auth; the handlers below just connect
// chi routes to that package, layer in the local-trusted gating that depends
// on peer-credential context keys (owned by this package), and host the few
// helpers that are inherently HTTP-layer (launch-redirect sanitization).
// Loopback/local-peer detection lives in peercred.go alongside the context keys
// it reads.

// registerAuthRoutes registers the public auth endpoints as raw chi routes.
// They are deliberately *not* huma operations: they set cookies, honor
// httprate middleware, and deal with non-JSON redirects.
func (srv *Server) registerAuthRoutes() {
	srv.router.Get("/api/auth/status", srv.handleAuthStatus)
}

func (srv *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	// With RUNWISP_NO_AUTH every caller is implicitly authenticated; the UI
	// reads auth_required=false and skips the login modal.
	authenticated := srv.noAuth || srv.auth.DecodeCookieToken(auth.TokenFromCookie(r))
	respondJSON(w, http.StatusOK, AuthStatusBody{
		AuthRequired:  !srv.noAuth,
		Authenticated: authenticated,
	})
}

// handleAuthChallenge mints a single-use nonce. Raw chi handler so it sits
// under the same httprate limiter as the login POST, preventing nonce-store
// flooding.
func (srv *Server) handleAuthChallenge(w http.ResponseWriter, r *http.Request) {
	nonce, err := srv.auth.IssueChallenge()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate challenge", err)
		return
	}
	respondJSON(w, http.StatusOK, AuthChallengeBody{Nonce: nonce})
}

// handleCreateLaunchTicket generates a launch ticket. The route sits in the
// protected group (authOrLocalTrusted), so the caller is already either a
// local-trusted peer (Unix socket / loopback) or the holder of a valid JWT —
// i.e. an already-authenticated session. That is the mint gate: a launch
// ticket only ever hands its bearer a session the minter could already obtain,
// so a remote TUI that authenticated via CHAP may mint one for its browser.
// The ticket itself stays single-use with a short TTL.
func (srv *Server) handleCreateLaunchTicket(w http.ResponseWriter, r *http.Request) {
	ticket, err := srv.auth.CreateLaunchTicket()
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to create launch ticket", err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"ticket": ticket})
}

// handleLaunchTicket redeems a single-use launch ticket and redirects to
// the UI with a session cookie. It accepts cross-origin requests so a remote
// operator's browser can redeem a ticket minted by an authenticated remote
// TUI; the protection is the ticket itself — single-use, short TTL, and only
// mintable by an already-authenticated (or local-trusted) session via
// handleCreateLaunchTicket. The route is rate-limited to bound guessing.
//
// An optional `redirect` query parameter targets a same-origin path (must
// start with `/` and not `//`) — used by the TUI's "download log" action so
// the operator's browser lands directly on the raw-log endpoint with a
// fresh session cookie. Cross-origin or scheme-relative redirects are
// rejected to avoid an open-redirect surface.
func (srv *Server) handleLaunchTicket(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		respondBadRequest(w, "Missing ticket parameter")
		return
	}
	if !srv.auth.ConsumeLaunchTicket(ticket) {
		respondUnauthorized(w, "Invalid or expired launch ticket")
		return
	}

	token, err := srv.auth.IssueToken(auth.JWTTokenDuration)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to generate token", err)
		return
	}
	srv.auth.SetAuthCookie(w, r, token, auth.JWTTokenDuration)
	http.Redirect(w, r, sanitizeLaunchRedirect(r.URL.Query().Get("redirect")), http.StatusSeeOther)
}

// sanitizeLaunchRedirect returns the requested redirect target if it is a
// safe same-origin absolute path; otherwise it falls back to "/". A safe
// target must parse as a URL with no scheme or host, and its path must start
// with a single "/" (not "//", which is scheme-relative).
//
// Backslashes are rejected: url.Parse leaves Host empty for a target like
// "/\evil.com", "/\/evil.com", or the percent-encoded "/%5Cevil.com" (which
// decodes into u.Path), but every major browser normalizes "\" to "/" in a
// Location header, turning it into "//evil.com" → an open redirect to
// https://evil.com. The check is on the decoded path so it catches the encoded
// form too. A backslash has no legitimate place in the same-origin paths this
// steers (dashboard routes, the raw-log endpoint), so dropping any that contain
// one closes the bypass without affecting real callers.
func sanitizeLaunchRedirect(target string) string { //NOSONAR: taint sanitized — url.Parse rejects scheme/host, backslashes rejected, only path is returned
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	p := u.Path
	if p == "" || p[0] != '/' || strings.ContainsRune(p, '\\') || (len(p) >= 2 && p[1] == '/') {
		return "/"
	}
	return p
}
