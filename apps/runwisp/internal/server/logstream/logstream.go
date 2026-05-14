// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package logstream drives a single SSE connection to a run's log. It
// resolves the requested anchor (`from` / `Last-Event-ID`) against
// on-disk backfill, then transitions into a live phase fed by the event
// bus. The parent server package owns route registration and HTTP shape;
// logstream owns the streaming state machine.
package logstream

import (
	"context"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

// PendingBufferLimit is the max LogLineEvent payloads buffered between
// the bus subscription and the disk-backfill drain. Once exceeded the
// streamer synthesizes a `dropped` event and evicts from the head — the
// live tail stays correct while old in-flight bursts are sacrificed first.
const PendingBufferLimit = 4096

// LineEvent is the per-line payload emitted on the SSE wire as the `line`
// event. It matches LogLineEntry's shape; the parent server package owns
// the named-type alias that huma/sse uses for event-name dispatch.
type LineEvent struct {
	N         int64  `json:"n" doc:"Absolute line number"`
	Ts        int64  `json:"ts" doc:"Unix milliseconds timestamp; 0 if unavailable"`
	Stream    string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text      string `json:"text" doc:"Line content without trailing newline"`
	Continued bool   `json:"continued,omitempty" doc:"True if this segment continues an oversized split line"`
}

// RotatedEvent fires when rotation has dropped lines below FirstAvailable.
type RotatedEvent struct {
	FirstAvailable int64 `json:"first_available" doc:"Lowest line number still on disk after rotation"`
}

// DroppedEvent fires when the bounded pending buffer overflowed.
type DroppedEvent struct {
	After int64 `json:"after" doc:"Highest line number observed before drops occurred"`
	Count int64 `json:"count" doc:"Number of line events dropped due to overflow"`
}

// DoneEvent is the final event of a stream. FinalLine is the last line
// emitted; Status echoes the reason ("ended").
type DoneEvent struct {
	FinalLine int64  `json:"final_line" doc:"Last line number emitted before the run terminated"`
	Status    string `json:"status" doc:"Reason the stream is closing (e.g. 'ended')"`
}

// EventEnvelope tells the caller how to wrap one of our event payloads in
// the parent package's huma/sse-typed events. The parent passes a Sender
// that knows the concrete named types; we keep the wire format plain.
//
// Senders close over the SSE writer; one Sender is constructed per
// connection. They must be safe for sequential use on the same
// goroutine.
type Sender interface {
	SendLine(LineEvent) error
	SendRotated(RotatedEvent) error
	SendDropped(DroppedEvent) error
	SendDone(DoneEvent) error
}

// RunGetter abstracts fetching a run by ID. Used by Run to re-check run
// state AFTER subscribing to the bus, closing the race where a run can
// finish between the handler's initial GetRun and the bus subscription.
type RunGetter interface {
	GetRun(string) (*model.Run, error)
}

// Run drives the SSE connection through three phases:
//  1. subscribe to the bus, draining published lines into a bounded buffer;
//  2. backfill from disk starting at the resolved anchor;
//  3. drain the buffer + follow live events until the run reaches a
//     terminal state.
//
// db lets us re-check run state after subscribing to close the race where
// a run completes between the caller's GetRun and Subscribe — without it
// we'd hang on the bus until context timeout. Tests pass nil to skip the
// recheck.
func Run(ctx context.Context, send Sender, logPath, runID string, bus events.EventBus, db RunGetter, anchorFrom, replayLimit int64, runEnded bool) {
	s := newStreamer(send, logPath)
	s.streamLoop(ctx, runID, bus, db, anchorFrom, replayLimit, runEnded)
}

// HumaSender adapts an huma sse.Sender into the Sender interface above,
// using callback wrappers that name each event type. The parent server
// package wires this so huma/sse can dispatch correct event names.
type HumaSender struct {
	Inner sse.Sender
	// Wrappers convert our wire-shape into the parent package's named
	// types (`LogLineSSEEvent`, etc.). They are pure aliases so the
	// generated OpenAPI keeps stable event names while logstream keeps
	// its own payload types.
	Line    func(LineEvent) any
	Rotated func(RotatedEvent) any
	Dropped func(DroppedEvent) any
	Done    func(DoneEvent) any
}

func (h HumaSender) SendLine(e LineEvent) error {
	return h.Inner(sse.Message{ID: int(e.N), Data: h.Line(e)})
}

func (h HumaSender) SendRotated(e RotatedEvent) error {
	return h.Inner(sse.Message{Data: h.Rotated(e)})
}

func (h HumaSender) SendDropped(e DroppedEvent) error {
	return h.Inner(sse.Message{Data: h.Dropped(e)})
}

func (h HumaSender) SendDone(e DoneEvent) error {
	return h.Inner(sse.Message{Data: h.Done(e)})
}

// streamer is the package-private state for one streaming connection.
type streamer struct {
	logPath  string
	send     Sender
	lastSent int64
}

func newStreamer(send Sender, logPath string) *streamer {
	return &streamer{
		logPath:  logPath,
		send:     send,
		lastSent: -1,
	}
}

// emitLine writes one line event and advances lastSent. Lines whose number
// is <= lastSent are silently skipped (dedupe across backfill ↔ live).
func (s *streamer) emitLine(line LineEvent) error {
	if line.N <= s.lastSent {
		return nil
	}
	if err := s.send.SendLine(line); err != nil {
		return err
	}
	s.lastSent = line.N
	return nil
}

func lineFromBus(le events.LogLineEvent) LineEvent {
	return LineEvent{
		N:         le.LineNum,
		Ts:        le.Timestamp,
		Stream:    le.Stream,
		Text:      le.Text,
		Continued: le.Continued,
	}
}

func lineFromRecord(rec logutil.LogLineRecord) LineEvent {
	return LineEvent{
		N:      rec.LineNum,
		Stream: rec.Stream,
		Text:   rec.Text,
	}
}

// resolveBackfillAnchor turns the user-requested `from` into a concrete
// starting line number. Negative values mean "tail from end". The result
// is clamped against the available rotation window.
func resolveBackfillAnchor(from, replayLimit, totalLines, firstAvailable int64) int64 {
	if from < 0 {
		anchor := totalLines + from
		if anchor < firstAvailable {
			anchor = firstAvailable
		}
		if anchor < 0 {
			anchor = 0
		}
		return anchor
	}
	if from < firstAvailable {
		from = firstAvailable
	}
	if replayLimit > 0 && totalLines-from > replayLimit {
		from = totalLines - replayLimit
		if from < firstAvailable {
			from = firstAvailable
		}
	}
	return from
}

func (s *streamer) streamLoop(ctx context.Context, runID string, bus events.EventBus, db RunGetter, anchorFrom, replayLimit int64, runEnded bool) {
	pendingCh := make(chan events.LogLineEvent, PendingBufferLimit)
	terminalCh := make(chan struct{}, 1)

	dropAfter := int64(-1)
	dropCount := int64(0)

	signalTerminal := func() {
		select {
		case terminalCh <- struct{}{}:
		default:
		}
	}

	unsubLine := bus.Subscribe(events.EventLogLine, func(e events.Event) {
		le, ok := e.Data.(events.LogLineEvent)
		if !ok || le.RunID != runID {
			return
		}
		select {
		case pendingCh <- le:
		default:
			// Bounded buffer overflowed — drop the oldest queued event so
			// we keep up with the live tail, then enqueue the newest.
			select {
			case dropped := <-pendingCh:
				if dropped.LineNum > dropAfter {
					dropAfter = dropped.LineNum
				}
				dropCount++
			default:
			}
			select {
			case pendingCh <- le:
			default:
				dropCount++
				if le.LineNum > dropAfter {
					dropAfter = le.LineNum
				}
			}
		}
	})
	defer unsubLine()

	terminalHandler := func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.Run == nil || re.Run.ID != runID {
			return
		}
		signalTerminal()
	}
	unsubCompleted := bus.Subscribe(events.EventRunCompleted, terminalHandler)
	defer unsubCompleted()
	unsubFailed := bus.Subscribe(events.EventRunFailed, terminalHandler)
	defer unsubFailed()

	// Backfill from disk so subscribers see history that was published
	// before they attached, plus everything between the anchor and the
	// live cursor.
	backfill, firstAvailable, totalLines, err := logutil.ReadLineRange(s.logPath, anchorFrom, replayLimit)
	if err != nil {
		slog.Warn("SSE: backfill read failed", "runID", runID, "err", err)
	}
	resolvedAnchor := resolveBackfillAnchor(anchorFrom, replayLimit, totalLines, firstAvailable)
	if firstAvailable > 0 {
		if err := s.send.SendRotated(RotatedEvent{FirstAvailable: firstAvailable}); err != nil {
			return
		}
	}
	for _, rec := range backfill {
		if rec.LineNum < resolvedAnchor {
			continue
		}
		if err := s.emitLine(lineFromRecord(rec)); err != nil {
			return
		}
	}

	// Race recheck: if the caller saw a non-terminal status but the run
	// completed before our Subscribe attached, the terminal event fired
	// into the void. db reads finalize that case so we don't hang.
	terminal := runEnded
	if !terminal && db != nil {
		if r, getErr := db.GetRun(runID); getErr == nil && r != nil && r.Status == model.PhaseEnded {
			terminal = true
		}
	}
	drainPending := func() error {
		for {
			select {
			case le := <-pendingCh:
				if err := s.emitLine(lineFromBus(le)); err != nil {
					return err
				}
			default:
				return nil
			}
		}
	}

	if terminal {
		if err := drainPending(); err != nil {
			return
		}
		if dropCount > 0 {
			_ = s.send.SendDropped(DroppedEvent{After: dropAfter, Count: dropCount})
		}
		_ = s.send.SendDone(DoneEvent{FinalLine: s.lastSent, Status: "ended"})
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-terminalCh:
			if err := drainPending(); err != nil {
				return
			}
			if dropCount > 0 {
				_ = s.send.SendDropped(DroppedEvent{After: dropAfter, Count: dropCount})
			}
			_ = s.send.SendDone(DoneEvent{FinalLine: s.lastSent, Status: "ended"})
			return
		case le := <-pendingCh:
			if err := s.emitLine(lineFromBus(le)); err != nil {
				return
			}
			if dropCount > 0 {
				if err := s.send.SendDropped(DroppedEvent{After: dropAfter, Count: dropCount}); err != nil {
					return
				}
				dropCount = 0
				dropAfter = -1
			}
		}
	}
}
