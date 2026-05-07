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

// LogStreamMsgKind discriminates the line-stream message types delivered to
// callers of StreamLogLines.
type LogStreamMsgKind int

const (
	LogStreamMsgKindLine LogStreamMsgKind = iota
	LogStreamMsgKindRotated
	LogStreamMsgKindDropped
	LogStreamMsgKindDone
	LogStreamMsgKindErr
)

// LogStreamMsg is a tagged union of events emitted on the line-based SSE
// channel. Exactly one of the fields matching Kind is populated; others are
// the zero value.
type LogStreamMsg struct {
	Kind     LogStreamMsgKind
	Line     LogLine
	Rotated  LogStreamRotated
	Dropped  LogStreamDropped
	Done     LogStreamDone
	ErrValue error
}

type LogStreamRotated struct {
	FirstAvailable int64 `json:"first_available"`
}

type LogStreamDropped struct {
	After int64 `json:"after"`
	Count int64 `json:"count"`
}

type LogStreamDone struct {
	FinalLine int64  `json:"final_line"`
	Status    string `json:"status"`
}

// StreamLogOpts tunes the line-stream subscription.
type StreamLogOpts struct {
	// FromLine is the absolute line anchor; negative values count from end
	// (e.g. -1000 returns the last 1000 lines as backfill).
	FromLine int64
	// ReplayLimit caps the backfill page size; 0 lets the server pick.
	ReplayLimit int64
}

// StreamLogLines opens the line-based SSE log stream. Each delivered message
// is one of the documented kinds (line / rotated / dropped / done / err).
// The channel is closed after a Done message, after Err, or after ctx is
// cancelled.
func (c *Client) StreamLogLines(ctx context.Context, taskName, runID string, opts StreamLogOpts) (<-chan LogStreamMsg, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log/stream?from=%d", taskName, runID, opts.FromLine)
	if opts.ReplayLimit > 0 {
		path += fmt.Sprintf("&replay_limit=%d", opts.ReplayLimit)
	}
	resp, err := c.doSSE(ctx, path)
	if err != nil {
		return nil, err
	}

	ch := make(chan LogStreamMsg, 64)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// SSE data lines for backfill burst can carry up to a 64 KiB log
		// line plus JSON overhead; 256 KiB is a comfortable headroom.
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

		var event string
		var dataParts []string

		flush := func() {
			defer func() {
				event = ""
				dataParts = dataParts[:0]
			}()
			if len(dataParts) == 0 {
				return
			}
			data := strings.Join(dataParts, "\n")
			msg, ok := parseLogStreamFrame(event, data)
			if !ok {
				return
			}
			select {
			case ch <- msg:
			case <-ctx.Done():
			}
		}

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				flush()
				continue
			}
			if strings.HasPrefix(line, "id: ") {
				continue
			}
			if strings.HasPrefix(line, "event: ") {
				event = strings.TrimPrefix(line, "event: ")
				continue
			}
			if strings.HasPrefix(line, "data: ") {
				dataParts = append(dataParts, strings.TrimPrefix(line, "data: "))
				continue
			}
			// retry: / : (comment) / unknown — ignore.
		}
		if err := scanner.Err(); err != nil {
			select {
			case ch <- LogStreamMsg{Kind: LogStreamMsgKindErr, ErrValue: err}:
			case <-ctx.Done():
			}
		}
	}()

	return ch, nil
}

// parseLogStreamFrame decodes one SSE event into a LogStreamMsg. The default
// (empty event name) is "line" per the SSE spec, but our server always names
// events explicitly; an unrecognised event is silently dropped (returns
// ok=false).
func parseLogStreamFrame(event, data string) (LogStreamMsg, bool) {
	switch event {
	case "line", "":
		var line LogLine
		if err := json.Unmarshal([]byte(data), &line); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindLine, Line: line}, true
	case "rotated":
		var r LogStreamRotated
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindRotated, Rotated: r}, true
	case "dropped":
		var d LogStreamDropped
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindDropped, Dropped: d}, true
	case "done":
		var d LogStreamDone
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindDone, Done: d}, true
	}
	return LogStreamMsg{}, false
}

// RunStreamEvent represents a parsed SSE event from the runs stream.
type RunStreamEvent struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}
