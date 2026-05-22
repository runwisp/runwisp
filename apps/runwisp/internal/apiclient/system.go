// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
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
	var samples []model.MetricsSample
	if err := c.doJSON("GET", "/api/system/history", nil, &samples); err != nil {
		return nil, err
	}
	return samples, nil
}

func (c *Client) GetRunSummary() (*sqlcdb.RunSummary, error) {
	var summary sqlcdb.RunSummary
	if err := c.doJSON("GET", "/api/runs/summary", nil, &summary); err != nil {
		return nil, err
	}
	return &summary, nil
}

func (c *Client) GetDaemonInfo() (*model.DaemonInfo, error) {
	var info model.DaemonInfo
	if err := c.doJSON("GET", "/api/info", nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

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
			var payload struct {
				Line string `json:"line"`
			}
			if err := json.Unmarshal([]byte(data), &payload); err != nil {
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
