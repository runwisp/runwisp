// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// GetSystemStats fetches system resource metrics.
func (c *Client) GetSystemStats() (*model.SystemStats, error) {
	var stats model.SystemStats
	if err := c.doJSON("GET", "/api/system", nil, &stats); err != nil {
		return nil, err
	}
	return &stats, nil
}

// GetMetricsHistory fetches historical system metrics from the ring buffer.
func (c *Client) GetMetricsHistory() ([]model.MetricsSample, error) {
	var samples []model.MetricsSample
	if err := c.doJSON("GET", "/api/system/history", nil, &samples); err != nil {
		return nil, err
	}
	return samples, nil
}

// RunSummary holds aggregate run statistics from the API.
type RunSummary struct {
	Total       int64   `json:"total"`
	Success     int64   `json:"success"`
	Failed      int64   `json:"failed"`
	LastFailure *string `json:"last_failure,omitempty"`
}

// GetRunSummary fetches aggregate run statistics.
func (c *Client) GetRunSummary() (*RunSummary, error) {
	var summary RunSummary
	if err := c.doJSON("GET", "/api/runs/summary", nil, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

// GetDaemonInfo fetches the daemon's identity and configuration summary.
func (c *Client) GetDaemonInfo() (*model.DaemonInfo, error) {
	var info model.DaemonInfo
	if err := c.doJSON("GET", "/api/info", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// HealthCheck pings the daemon's health endpoint.
func (c *Client) HealthCheck() error {
	resp, err := c.doRaw("GET", "/health")
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// StreamDaemonLogs opens an SSE connection to /api/daemon/log-stream and
// delivers log lines on the returned channel. The channel is closed when the
// context is cancelled or the stream ends.
func (c *Client) StreamDaemonLogs(ctx context.Context) (<-chan string, error) {
	resp, err := c.doSSE(ctx, "/api/daemon/log-stream")
	if err != nil {
		return nil, err
	}

	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var logLine string
			if err := json.Unmarshal([]byte(data), &logLine); err != nil {
				logLine = data
			}
			select {
			case ch <- logLine:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}
