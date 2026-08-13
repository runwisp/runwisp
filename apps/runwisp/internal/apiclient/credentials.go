// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"errors"
	"net/http"
)

// LocalCredentials carries the daemon's ephemeral password as returned by
// GET /api/local/credentials. The endpoint is only available on the Unix
// socket — callers using a TCP client will always receive 403.
type LocalCredentials struct {
	Password  string
	Ephemeral bool
}

// ErrLocalCredentialsUnavailable signals the daemon is configured with
// RUNWISP_PASSWORD and refuses to disclose it. Distinct from ErrUnauthorized
// so callers can render a useful "ask the operator" message instead of
// treating it as a generic auth failure.
var ErrLocalCredentialsUnavailable = errors.New("no ephemeral password to disclose")

// ErrAuthDisabled signals the daemon runs with RUNWISP_AUTH=off — there is no
// password in play at all. Distinct from ErrLocalCredentialsUnavailable so
// callers don't mislead the operator into hunting for an env-var value.
var ErrAuthDisabled = errors.New("daemon runs with authentication disabled")

// GetLocalCredentials fetches the ephemeral password over the local socket.
// Returns ErrLocalCredentialsUnavailable when the daemon refuses disclosure
// (env-var case) and ErrAuthDisabled when the daemon runs with
// RUNWISP_AUTH=off. Any other non-2xx surfaces as the underlying HTTP error so
// the caller can distinguish "not on a socket" (403) from real transport
// failures.
func (c *Client) GetLocalCredentials() (*LocalCredentials, error) {
	var body struct {
		Password  string `json:"password"`
		Ephemeral bool   `json:"ephemeral"`
	}
	if err := c.doJSON("GET", "/api/local/credentials", nil, &body); err != nil {
		if IsHTTPStatus(err, http.StatusNotFound) {
			return nil, ErrLocalCredentialsUnavailable
		}
		if IsHTTPStatus(err, http.StatusConflict) {
			return nil, ErrAuthDisabled
		}
		return nil, err
	}
	return &LocalCredentials{Password: body.Password, Ephemeral: body.Ephemeral}, nil
}
