// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/logstream"
)

// StreamRunEvents opens an SSE connection to the unified /api/events/stream feed and
// delivers parsed run events on the returned channel. The stream also carries
// system/config/notification events, which the parser ignores. The channel is
// closed when the context is cancelled or the stream ends.
//
// lastEventID, when non-empty, is sent as the lastEventId query param so a
// reconnecting caller resumes the replay from the last event it saw instead
// of silently losing everything that fired during the gap — pass the ID
// field of the last RunStreamEvent this caller received.
func (c *Client) StreamRunEvents(ctx context.Context, lastEventID string) (<-chan RunStreamEvent, error) {
	path := "/api/events/stream"
	if lastEventID != "" {
		path += "?lastEventId=" + lastEventID
	}
	resp, err := c.doSSE(ctx, path)
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

// StreamLogLines opens the line-based SSE log stream. Each delivered message
// is one of the documented kinds (line / rotated / dropped / done / err).
// The channel is closed after a Done message, after Err, or after ctx is
// cancelled. fromLine is the absolute line anchor; negative values count
// from the end (e.g. -1000 returns the last 1000 lines as backfill).
func (c *Client) StreamLogLines(ctx context.Context, runID string, fromLine int64) (<-chan LogStreamMsg, error) {
	path := fmt.Sprintf("/api/runs/%s/log/stream?from=%d", runID, fromLine)
	resp, err := c.doSSE(ctx, path)
	if err != nil {
		return nil, err
	}

	ch := make(chan LogStreamMsg, 64)
	go streamLogLinesLoop(ctx, resp.Body, ch)
	return ch, nil
}

func streamLogLinesLoop(ctx context.Context, body io.ReadCloser, ch chan<- LogStreamMsg) {
	defer close(ch)
	defer body.Close()

	send := func(msg LogStreamMsg) bool {
		select {
		case ch <- msg:
			return true
		case <-ctx.Done():
			return false
		}
	}
	err := readSSEFrames(body, func(f sseFrame) bool {
		if msg, ok := parseLogStreamFrame(f.event, f.data); ok {
			return send(msg)
		}
		return true
	})
	if err != nil {
		send(LogStreamMsg{Kind: LogStreamMsgKindErr, ErrValue: err})
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

// RunStreamEvent is the client-side SSE dispatch frame: event type + raw JSON
// payload, plus the frame's id (empty when the server sent none — pings and
// notification updates carry no id and stay out of the resume sequence).
type RunStreamEvent struct {
	Type string
	ID   string
	Data json.RawMessage
}

// simpleSSELoop sends every named SSE frame from body on ch until the body
// closes or ctx cancels. Unnamed frames are dropped.
func simpleSSELoop(ctx context.Context, body io.ReadCloser, ch chan<- RunStreamEvent) {
	defer close(ch)
	defer body.Close()

	_ = readSSEFrames(body, func(f sseFrame) bool {
		if f.event == "" {
			return true
		}
		select {
		case ch <- RunStreamEvent{Type: f.event, ID: f.id, Data: json.RawMessage(f.data)}:
			return true
		case <-ctx.Done():
			return false
		}
	})
}
