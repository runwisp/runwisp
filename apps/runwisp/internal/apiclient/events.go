// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// StreamRunEvents opens an SSE connection to /api/runs/stream and delivers
// parsed events on the returned channel. The channel is closed when the
// context is cancelled or the stream ends.
func (c *Client) StreamRunEvents(ctx context.Context) (<-chan RunStreamEvent, error) {
	resp, err := c.doSSE(ctx, "/api/runs/stream")
	if err != nil {
		return nil, err
	}

	ch := make(chan RunStreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		var eventType string

		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event: ") {
				eventType = strings.TrimPrefix(line, "event: ")
				continue
			}

			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				if eventType != "" {
					evt := RunStreamEvent{
						Type: eventType,
						Data: json.RawMessage(data),
					}
					select {
					case ch <- evt:
					case <-ctx.Done():
						return
					}
					eventType = ""
				}
				continue
			}

			// Empty line or comment — reset.
			if line == "" {
				eventType = ""
			}
		}
	}()

	return ch, nil
}

// StreamLog opens an SSE connection to the log-stream endpoint and delivers
// raw log chunks on the returned channel.
func (c *Client) StreamLog(ctx context.Context, taskName string, runID string) (<-chan string, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log-stream", taskName, runID)
	resp, err := c.doSSE(ctx, path)
	if err != nil {
		return nil, err
	}

	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// JSON-encoding a 1 MB raw chunk adds overhead (newline escaping, etc.)
		// so the SSE data line can exceed 1 MB.  Use 4 MB to be safe.
		scanner.Buffer(make([]byte, 0, 4*1024*1024), 4*1024*1024)

		skipNextData := false
		for scanner.Scan() {
			line := scanner.Text()

			if strings.HasPrefix(line, "event: done") {
				return
			}

			// Non-log SSE events (e.g. "event: metadata") should be skipped
			// along with their subsequent data line.
			if strings.HasPrefix(line, "event: ") && !strings.HasPrefix(line, "event: log") {
				skipNextData = true
				continue
			}

			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			if skipNextData {
				skipNextData = false
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			// Log stream sends JSON-encoded strings.
			var chunk string
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				// Treat as raw text if not valid JSON.
				chunk = data
			}

			select {
			case ch <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch, nil
}

// GetLog fetches the full log content for a run.
func (c *Client) GetLog(taskName string, runID string) (string, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log", taskName, runID)
	resp, err := c.doRaw("GET", path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var b strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)
	for scanner.Scan() {
		b.WriteString(scanner.Text())
		b.WriteByte('\n')
	}
	return b.String(), scanner.Err()
}

// RunStreamEvent represents a parsed SSE event from the runs stream.
type RunStreamEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}
