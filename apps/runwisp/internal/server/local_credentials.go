// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"log/slog"
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
//     including JWT-authenticated TCP callers and launch-ticket browser
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
			"Always 403 over TCP — even with a valid JWT. Always 404 when the daemon is configured with RUNWISP_PASSWORD.",
		Tags: []string{"System"},
	}, srv.handleGetLocalCredentials)
}
