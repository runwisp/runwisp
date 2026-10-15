// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
)

// LocalCredentialsBody is the JSON payload returned by GET /api/local/credentials.
//
// The endpoint is local-socket only and refuses to disclose env-var-supplied
// passwords. When the operator set RUNWISP_PASSWORD, the handler returns 404
// rather than an empty body — a buggy client treating "empty string" as
// "no password" cannot accidentally claim there is none when there is one.
type LocalCredentialsBody struct {
	Password  string `json:"password" doc:"Ephemeral password generated in memory at boot. Omitted unless ephemeral=true."`
	Ephemeral bool   `json:"ephemeral" doc:"Always true on success — the endpoint refuses to return env-var-supplied passwords."`
}

// LocalCredentialsOutput wraps LocalCredentialsBody with a Cache-Control
// response header so intermediaries (and any future logging middleware) do
// not retain the value.
type LocalCredentialsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LocalCredentialsBody
}

// handleGetLocalCredentials returns the daemon's ephemeral password to a
// caller that arrived on the Unix socket. Two handler-level gates back the
// route-level authOrLocalTrusted middleware:
//
//   - Gate 1 (IsLocalTrustedCtx) rejects anything not on the Unix listener,
//     including session-authenticated TCP callers and launch-ticket browser
//     sessions. This is what makes the endpoint impossible to reach over the
//     network even with a valid login.
//   - Gate 2 refuses to disclose env-var-supplied passwords by status code
//     (404) rather than by empty field. The operator set RUNWISP_PASSWORD
//     specifically to keep that value out of the daemon's API surface; the
//     daemon's job is to verify it, not redistribute it.
func (srv *Server) handleGetLocalCredentials(ctx context.Context, _ *struct{}) (*LocalCredentialsOutput, error) {
	if !IsLocalTrustedCtx(ctx) {
		return nil, huma.Error403Forbidden("local-credentials endpoint is only available on the Unix socket")
	}
	// RUNWISP_AUTH=off: a password is still minted internally to keep the
	// cookie machinery alive, but it gates nothing — handing it out would
	// present a non-credential as a credential.
	if srv.noAuth {
		return nil, huma.Error409Conflict("daemon is running with RUNWISP_AUTH=off; there is no password to retrieve")
	}
	if !srv.passwordEphemeral {
		return nil, huma.Error404NotFound("daemon is configured with RUNWISP_PASSWORD; no shareable password is disclosed")
	}
	slog.Info("Ephemeral password retrieved via socket")
	return &LocalCredentialsOutput{
		CacheControl: "no-store",
		Body: LocalCredentialsBody{
			Password:  srv.auth.Password(),
			Ephemeral: true,
		},
	}, nil
}

// registerLocalCredentialsRoute wires GET /api/local/credentials into the
// protected huma API. Defense-in-depth: the route lives behind
// authOrLocalTrusted; the handler then re-checks the local-trusted flag and
// the ephemeral guard.
func (srv *Server) registerLocalCredentialsRoute(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "getLocalCredentials",
		Method:      http.MethodGet,
		Path:        "/api/local/credentials",
		Summary:     "Retrieve the daemon's ephemeral password (Unix socket only)",
		Description: "Returns the in-memory ephemeral password to a local CLI/TUI client arriving on the Unix socket. " +
			"Always 403 over TCP — even with a valid session token. Always 404 when the daemon is configured with RUNWISP_PASSWORD.",
		Tags: []string{"System"},
	}, srv.handleGetLocalCredentials)
}

// handleMintViewerPassword returns the read-only password, minting a random
// in-memory one on first call so `runwisp password --viewer` can enable read-only
// access on a running daemon. Same gates as handleGetLocalCredentials: Unix
// socket only, nothing to hand out under RUNWISP_AUTH=off, and a password the
// operator supplied through RUNWISP_VIEWER_PASSWORD is never disclosed (404).
func (srv *Server) handleMintViewerPassword(ctx context.Context, _ *struct{}) (*LocalCredentialsOutput, error) {
	if !IsLocalTrustedCtx(ctx) {
		return nil, huma.Error403Forbidden("local-credentials endpoint is only available on the Unix socket")
	}
	if srv.noAuth {
		return nil, huma.Error409Conflict("daemon is running with RUNWISP_AUTH=off; there is no password to retrieve")
	}
	if srv.viewerFromEnv {
		return nil, huma.Error404NotFound("daemon is configured with RUNWISP_VIEWER_PASSWORD; no shareable password is disclosed")
	}
	pw, err := srv.auth.MintViewerPassword()
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to generate viewer password", err)
	}
	slog.Info("Ephemeral read-only password retrieved via socket")
	return &LocalCredentialsOutput{
		CacheControl: "no-store",
		Body:         LocalCredentialsBody{Password: pw, Ephemeral: true},
	}, nil
}

func (srv *Server) registerViewerPasswordRoute(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mintViewerPassword",
		Method:      http.MethodPost,
		Path:        "/api/local/viewer-password",
		Summary:     "Enable and retrieve the read-only password (Unix socket only)",
		Description: "Returns the in-memory read-only password, generating one on first call. Sessions opened with it can view everything and change nothing. " +
			"Always 403 over TCP. Always 404 when the daemon is configured with RUNWISP_VIEWER_PASSWORD.",
		Tags: []string{"System"},
	}, srv.handleMintViewerPassword)
}
