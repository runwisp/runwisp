// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
)

func (c *Client) GetSystemStats() (*model.SystemStats, error) {
	var stats model.SystemStats
	if err := c.doJSON("GET", "/api/system", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// GetMetricsHistory fetches historical system metrics from the ring buffer.
func (c *Client) GetMetricsHistory() ([]model.MetricsSample, error) {
	var resp server.MetricsHistoryBody
	if err := c.doJSON("GET", "/api/system/history", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (c *Client) GetRunSummary() (*model.RunSummary, error) {
	var summary model.RunSummary
	if err := c.doJSON("GET", "/api/runs/summary", nil, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (c *Client) GetDaemonInfo() (*model.DaemonInfo, error) {
	var info model.DaemonInfo
	if err := c.doJSON("GET", "/api/daemon", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetInstanceInfo fetches the daemon's local identity (datadir, config, socket,
// pid, version) from GET /api/daemon/identity. It is used by a launcher that hit a
// port conflict to discover whether a RunWisp daemon holds the port and where
// it lives. The endpoint is public but local-gated, so a no-password TCP client
// reaches it over loopback; a non-RunWisp port-holder yields a transport or
// decode error, which the caller treats as "not a discoverable daemon".
func (c *Client) GetInstanceInfo() (*model.InstanceInfo, error) {
	var info model.InstanceInfo
	if err := c.doJSON("GET", "/api/daemon/identity", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Reload asks the daemon to re-read runwisp.toml and reconcile its live task
// set, returning the applied diff. A rejected reload (bad config or a
// restart-only change) comes back as an error from the daemon.
func (c *Client) Reload() (*model.ReloadResult, error) {
	var result model.ReloadResult
	if err := c.doJSON("POST", "/api/reload", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AuthStatus reports whether the daemon requires authentication, via the public
// GET /api/auth/status endpoint. A remote client probes it before prompting for
// a password so a RUNWISP_AUTH=off daemon connects without one.
func (c *Client) AuthStatus() (server.AuthStatusBody, error) {
	var body server.AuthStatusBody
	if err := c.doJSON("GET", "/api/auth/status", nil, &body); err != nil {
		return server.AuthStatusBody{}, err
	}
	return body, nil
}

func (c *Client) HealthCheck() error {
	resp, err := c.doRaw("/health")
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
