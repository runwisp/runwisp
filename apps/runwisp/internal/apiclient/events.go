// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/server/logstream"
)

const (
	ssePrefixEvent = "event: "
	ssePrefixData  = "data: "
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
	go simpleSSELoop(ctx, resp.Body, ch)
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
	Line     server.LogLineEntry
	Rotated  logstream.RotatedEvent
	Dropped  logstream.DroppedEvent
	Done     logstream.DoneEvent
	ErrValue error
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
	go c.streamLogLinesLoop(ctx, resp.Body, ch)
	return ch, nil
}

func (c *Client) streamLogLinesLoop(ctx context.Context, body io.ReadCloser, ch chan<- LogStreamMsg) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
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
		if strings.HasPrefix(line, ssePrefixEvent) {
			event = strings.TrimPrefix(line, ssePrefixEvent)
			continue
		}
		if strings.HasPrefix(line, ssePrefixData) {
			dataParts = append(dataParts, strings.TrimPrefix(line, ssePrefixData))
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
}

// parseLogStreamFrame decodes one SSE event into a LogStreamMsg. The default
// (empty event name) is "line" per the SSE spec, but our server always names
// events explicitly; an unrecognised event is silently dropped (returns
// ok=false).
func parseLogStreamFrame(event, data string) (LogStreamMsg, bool) {
	switch event {
	case "line", "":
		var line server.LogLineEntry
		if err := json.Unmarshal([]byte(data), &line); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindLine, Line: line}, true
	case "rotated":
		var r logstream.RotatedEvent
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindRotated, Rotated: r}, true
	case "dropped":
		var d logstream.DroppedEvent
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindDropped, Dropped: d}, true
	case "done":
		var d logstream.DoneEvent
		if err := json.Unmarshal([]byte(data), &d); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindDone, Done: d}, true
	}
	return LogStreamMsg{}, false
}

// SSEEvent is the client-side SSE dispatch frame: event type + raw JSON payload.
type SSEEvent struct {
	Type string
	Data json.RawMessage
}

// RunStreamEvent is an SSEEvent from the /api/runs/stream endpoint.
type RunStreamEvent = SSEEvent

// simpleSSELoop is the shared parse loop for SSE streams that deliver
// single-line data frames. It reads lines from body, pairs event:/data: into
// SSEEvent values, and sends them on ch until the body closes or ctx cancels.
func simpleSSELoop(ctx context.Context, body io.ReadCloser, ch chan<- SSEEvent) {
	defer close(ch)
	defer body.Close()

	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, ssePrefixEvent) {
			eventType = strings.TrimPrefix(line, ssePrefixEvent)
			continue
		}

		if strings.HasPrefix(line, ssePrefixData) {
			data := strings.TrimPrefix(line, ssePrefixData)
			if eventType != "" {
				select {
				case ch <- SSEEvent{Type: eventType, Data: json.RawMessage(data)}:
				case <-ctx.Done():
					return
				}
				eventType = ""
			}
			continue
		}

		if line == "" {
			eventType = ""
		}
	}
}
