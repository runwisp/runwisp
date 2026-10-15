// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/auth"
)

// This file is the transport adapter for the auth subsystem: it wires huma
// operations to the core CHAP/session flow in internal/server/auth, adds the
// local-trusted / secure-request context lookups that depend on keys owned by
// this package, and hosts the one helper that's inherently HTTP-layer
// (launch-redirect sanitization). Loopback/local-peer detection lives in
// peercred.go alongside the context keys it reads.

// registerAuthRoutes registers GET /api/auth/status directly onto the
// server's public huma API. It carries no rate limit (there's nothing to
// flood — it only reads a cookie) and sits outside the protected group (the
// UI calls it before it knows whether the caller is authenticated).
func (srv *Server) registerAuthRoutes() {
	huma.Register(srv.api, huma.Operation{
		OperationID: "getAuthStatus",
		Method:      http.MethodGet,
		Path:        "/api/auth/status",
		Summary:     "Check whether the caller is authenticated",
		Tags:        []string{"Auth"},
	}, srv.humaAuthStatus)
}

func (srv *Server) humaAuthStatus(_ context.Context, input *AuthStatusInput) (*AuthStatusOutput, error) {
	// With RUNWISP_AUTH=off every caller is implicitly authenticated; the UI
	// reads authRequired=false and skips the login modal.
	readOnly, ok := srv.auth.VerifyToken(input.Token)
	return &AuthStatusOutput{Body: AuthStatusBody{
		AuthRequired:  !srv.noAuth,
		Authenticated: srv.noAuth || ok,
		ReadOnly:      ok && readOnly,
	}}, nil
}

// authSubAPI returns a huma API on r, a chi sub-router carrying one of the auth
// limiters, sharing the same OpenAPI document as the rest of the API — the
// pattern established by registerProtectedHumaRoutes.
func (srv *Server) authSubAPI(r chi.Router) huma.API {
	cfg := huma.DefaultConfig("", "")
	cfg.OpenAPI = srv.api.OpenAPI()
	return humachi.New(r, cfg)
}

// registerAuthChallengeRoute wires the challenge endpoint onto r, which carries
// a plain per-IP request cap.
func (srv *Server) registerAuthChallengeRoute(r chi.Router) {
	huma.Register(srv.authSubAPI(r), huma.Operation{
		OperationID: "getAuthChallenge",
		Method:      http.MethodGet,
		Path:        "/api/auth/challenge",
		Summary:     "Mint a single-use CHAP challenge nonce",
		Tags:        []string{"Auth"},
	}, srv.humaAuthChallenge)
}

// registerAuthLoginRoutes wires login and launch-ticket redeem onto r, which
// carries the failure lockout.
func (srv *Server) registerAuthLoginRoutes(r chi.Router) {
	loginAPI := srv.authSubAPI(r)

	huma.Register(loginAPI, huma.Operation{
		OperationID:  "login",
		Method:       http.MethodPost,
		Path:         "/api/auth/login",
		Summary:      "Exchange a signed CHAP challenge response for a session",
		Tags:         []string{"Auth"},
		MaxBodyBytes: auth.MaxRequestBodySize,
	}, srv.humaLogin)

	huma.Register(loginAPI, huma.Operation{
		OperationID: "redeemLaunchTicket",
		Method:      http.MethodGet,
		Path:        "/api/auth/launch-ticket",
		Summary:     "Redeem a single-use launch ticket and start a browser session",
		Description: "Consumes the ticket, sets the session cookie, and redirects (303) to the " +
			"`redirect` query parameter (a same-origin path; defaults to \"/\", and falls back to " +
			"\"/\" if the value is unsafe).",
		Tags: []string{"Auth"},
	}, srv.humaRedeemLaunchTicket)
}

// humaAuthChallenge mints a single-use nonce. Its route carries a per-IP
// request cap, preventing nonce-store flooding.
func (srv *Server) humaAuthChallenge(_ context.Context, _ *struct{}) (*AuthChallengeOutput, error) {
	nonce, err := srv.auth.IssueChallenge()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to generate challenge", err)
	}
	return &AuthChallengeOutput{Body: AuthChallengeBody{Nonce: nonce}}, nil
}

func (srv *Server) humaLogin(ctx context.Context, input *AuthLoginInput) (*AuthLoginOutput, error) {
	token, readOnly, err := srv.auth.Login(input.Body.Nonce, input.Body.Response)
	if errors.Is(err, auth.ErrInvalidNonce) {
		return nil, huma.Error401Unauthorized("Invalid or expired challenge")
	}
	if err != nil {
		return nil, huma.Error401Unauthorized("Invalid password")
	}
	srv.recordSignIn(ctx)
	return &AuthLoginOutput{
		SetCookie: srv.auth.BuildAuthCookie(token, auth.SessionDuration, isSecureCtx(ctx)),
		Body:      AuthLoginBody{Token: token, ReadOnly: readOnly},
	}, nil
}

// registerCreateLaunchTicketRoute wires POST /api/auth/launch-ticket (mint)
// onto api. Called from registerProtectedHumaRoutes since it shares that
// group's middleware — see humaCreateLaunchTicket's doc comment for why that
// group is the correct mint gate.
func (srv *Server) registerCreateLaunchTicketRoute(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "createLaunchTicket",
		Method:      http.MethodPost,
		Path:        "/api/auth/launch-ticket",
		Summary:     "Mint a single-use launch ticket",
		Description: "The ticket can be redeemed via GET /api/auth/launch-ticket?ticket=<ticket>.",
		Tags:        []string{"Auth"},
	}, srv.humaCreateLaunchTicket)
}

// humaCreateLaunchTicket generates a launch ticket. The route sits in the
// protected group (authOrLocalTrusted), so the caller is already either a
// local-trusted peer (Unix socket / loopback) or the holder of a valid session —
// i.e. an already-authenticated session. That is the mint gate: a launch
// ticket only ever hands its bearer a session the minter could already obtain,
// so a remote TUI that authenticated via CHAP may mint one for its browser.
// The ticket itself stays single-use with a short TTL.
func (srv *Server) humaCreateLaunchTicket(_ context.Context, _ *struct{}) (*LaunchTicketMintOutput, error) {
	ticket, err := srv.auth.CreateLaunchTicket()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to create launch ticket", err)
	}
	return &LaunchTicketMintOutput{Body: LaunchTicketBody{Ticket: ticket}}, nil
}

// humaRedeemLaunchTicket redeems a single-use launch ticket and redirects to
// the UI with a session cookie. It accepts cross-origin requests so a remote
// operator's browser can redeem a ticket minted by an authenticated remote
// TUI; the protection is the ticket itself — single-use, short TTL, and only
// mintable by an already-authenticated (or local-trusted) session via
// humaCreateLaunchTicket. Failed redemptions share login's lockout.
//
// The optional `redirect` query parameter targets a same-origin path (must
// start with `/` and not `//`) — used by the TUI's "download log" action so
// the operator's browser lands directly on the raw-log endpoint with a
// fresh session cookie. Cross-origin or scheme-relative redirects are
// rejected to avoid an open-redirect surface.
func (srv *Server) humaRedeemLaunchTicket(ctx context.Context, input *LaunchTicketRedeemInput) (*LaunchTicketRedeemOutput, error) {
	if !srv.auth.ConsumeLaunchTicket(input.Ticket) {
		return nil, huma.Error401Unauthorized("Invalid or expired launch ticket")
	}

	// Not a sign-in for the notification link base: the local TUI redeems its
	// tickets at localhost, which would point every notification link there.
	token := srv.auth.IssueToken(auth.SessionDuration)
	return &LaunchTicketRedeemOutput{
		Status:    http.StatusSeeOther,
		Location:  sanitizeLaunchRedirect(input.Redirect),
		SetCookie: srv.auth.BuildAuthCookie(token, auth.SessionDuration, isSecureCtx(ctx)),
	}, nil
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
