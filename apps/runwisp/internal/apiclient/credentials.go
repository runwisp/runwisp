// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

// GetLocalCredentials fetches the ephemeral password over the local socket.
// Returns ErrLocalCredentialsUnavailable when the daemon refuses disclosure
// (env-var case). Any other non-2xx surfaces as the underlying HTTP error so
// the caller can distinguish "not on a socket" (403) from "no password to
// share" (404) from real transport failures.
func (c *Client) GetLocalCredentials() (*LocalCredentials, error) {
	var body struct {
		Password  string `json:"password"`
		Ephemeral bool   `json:"ephemeral"`
	}
	if err := c.doJSON("GET", "/api/local/credentials", nil, &body); err != nil {
		if IsHTTPStatus(err, http.StatusNotFound) {
			return nil, ErrLocalCredentialsUnavailable
		}
		return nil, err
	}
	return &LocalCredentials{Password: body.Password, Ephemeral: body.Ephemeral}, nil
}
