// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

// StreamRunEvents opens an SSE connection to the unified /api/stream feed and
// delivers parsed run events on the returned channel. The stream also carries
// system/config/notification events, which the parser ignores. The channel is
// closed when the context is cancelled or the stream ends.
func (c *Client) StreamRunEvents(ctx context.Context) (<-chan RunStreamEvent, error) {
	resp, err := c.doSSE(ctx, "/api/stream")
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
	LogStreamMsgKindRegion
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
	Region   logstream.RegionEvent
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
		path += fmt.Sprintf("&replayLimit=%d", opts.ReplayLimit)
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

	acc := logStreamFrameAccumulator{}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if msg, ok := acc.flush(); ok {
				sendLogStreamMsg(ctx, ch, msg)
			}
			continue
		}
		acc.consume(line)
	}
	if err := scanner.Err(); err != nil {
		sendLogStreamMsg(ctx, ch, LogStreamMsg{Kind: LogStreamMsgKindErr, ErrValue: err})
	}
}

// logStreamFrameAccumulator buffers the event:/data: lines of a single SSE
// frame until a blank line completes it.
type logStreamFrameAccumulator struct {
	event     string
	dataParts []string
}

// consume folds one non-blank SSE line into the in-progress frame, ignoring
// id:/retry:/comment/unknown lines.
func (a *logStreamFrameAccumulator) consume(line string) {
	switch {
	case strings.HasPrefix(line, "id: "):
	case strings.HasPrefix(line, ssePrefixEvent):
		a.event = strings.TrimPrefix(line, ssePrefixEvent)
	case strings.HasPrefix(line, ssePrefixData):
		a.dataParts = append(a.dataParts, strings.TrimPrefix(line, ssePrefixData))
	}
	// retry: / : (comment) / unknown — ignore.
}

// flush parses the buffered frame into a LogStreamMsg, resets the accumulator,
// and reports whether a deliverable message was produced.
func (a *logStreamFrameAccumulator) flush() (LogStreamMsg, bool) {
	defer func() {
		a.event = ""
		a.dataParts = a.dataParts[:0]
	}()
	if len(a.dataParts) == 0 {
		return LogStreamMsg{}, false
	}
	data := strings.Join(a.dataParts, "\n")
	return parseLogStreamFrame(a.event, data)
}

// sendLogStreamMsg delivers msg on ch unless ctx is cancelled first.
func sendLogStreamMsg(ctx context.Context, ch chan<- LogStreamMsg, msg LogStreamMsg) {
	select {
	case ch <- msg:
	case <-ctx.Done():
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
	case "region":
		var rg logstream.RegionEvent
		if err := json.Unmarshal([]byte(data), &rg); err != nil {
			return LogStreamMsg{}, false
		}
		return LogStreamMsg{Kind: LogStreamMsgKindRegion, Region: rg}, true
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

// RunStreamEvent is an SSEEvent from the unified /api/stream endpoint.
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
