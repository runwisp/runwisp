// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
)

func (c *Client) GetSystemStats(ctx context.Context) (*model.SystemStats, error) {
	return doJSONAs[model.SystemStats](ctx, c, "GET", "/api/system", nil)
}

// GetMetricsHistory fetches historical system metrics from the ring buffer.
func (c *Client) GetMetricsHistory(ctx context.Context) ([]model.MetricsSample, error) {
	var resp server.MetricsHistoryBody
	if err := c.doJSON(ctx, "GET", "/api/system/metrics", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetRunSummary(ctx context.Context) (*model.RunSummary, error) {
	return doJSONAs[model.RunSummary](ctx, c, "GET", "/api/runs/summary", nil)
}

func (c *Client) GetDaemonInfo(ctx context.Context) (*model.DaemonInfo, error) {
	return doJSONAs[model.DaemonInfo](ctx, c, "GET", "/api/daemon", nil)
}

// GetInstanceInfo fetches the daemon's local identity (datadir, config, socket,
// pid, version) from GET /api/daemon/identity. It is used by a launcher that hit a
// port conflict to discover whether a RunWisp daemon holds the port and where
// it lives. The endpoint is public but local-gated, so a no-password TCP client
// reaches it over loopback; a non-RunWisp port-holder yields a transport or
// decode error, which the caller treats as "not a discoverable daemon".
func (c *Client) GetInstanceInfo(ctx context.Context) (*model.InstanceInfo, error) {
	return doJSONAs[model.InstanceInfo](ctx, c, "GET", "/api/daemon/identity", nil)
}

// Reload asks the daemon to re-read runwisp.toml and reconcile its live task
// set, returning the applied diff. A rejected reload (bad config or a
// restart-only change) comes back as an error from the daemon.
func (c *Client) Reload(ctx context.Context) (*model.ReloadResult, error) {
	return doJSONAs[model.ReloadResult](ctx, c, "POST", "/api/daemon/reload", nil)
}

// AuthStatus reports whether the daemon requires authentication, via the public
// GET /api/auth/status endpoint. A remote client probes it before prompting for
// a password so a RUNWISP_AUTH=off daemon connects without one.
func (c *Client) AuthStatus(ctx context.Context) (server.AuthStatusBody, error) {
	var body server.AuthStatusBody
	if err := c.doJSON(ctx, "GET", "/api/auth/status", nil, &body); err != nil {
		return server.AuthStatusBody{}, err
	}
	return body, nil
}

func (c *Client) HealthCheck(ctx context.Context) error {
	resp, err := c.doRequest(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// StreamDaemonLogs opens an SSE connection to /api/daemon/log/stream and
// delivers log lines on the returned channel. The channel is closed when the
// context is cancelled or the stream ends.
func (c *Client) StreamDaemonLogs(ctx context.Context) (<-chan string, error) {
	resp, err := c.doSSE(ctx, "/api/daemon/log/stream")
	if err != nil {
		return nil, err
	}

	events := make(chan SSEEvent, 64)
	go simpleSSELoop(ctx, resp.Body, events)

	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		for evt := range events {
			var payload struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal(evt.Data, &payload); err != nil {
				continue
			}
			select {
			case ch <- payload.Line:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
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
func (c *Client) GetLocalCredentials(ctx context.Context) (*server.LocalCredentialsBody, error) {
	var body server.LocalCredentialsBody
	if err := c.doJSON(ctx, "GET", "/api/local/credentials", nil, &body); err != nil {
		if IsHTTPStatus(err, http.StatusNotFound) {
			return nil, ErrLocalCredentialsUnavailable
		}
		if IsHTTPStatus(err, http.StatusConflict) {
			return nil, ErrAuthDisabled
		}
		return nil, err
	}
	return &body, nil
}
