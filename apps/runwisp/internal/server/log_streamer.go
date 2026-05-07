// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

// pendingBufferLimit is the max LogLineEvent payloads buffered between the
// bus subscription and the disk-backfill drain. Once exceeded we synthesize
// an `event: dropped` and evict from the head — the live tail stays correct
// while old in-flight bursts are sacrificed first.
const pendingBufferLimit = 4096

// runGetter abstracts fetching a run by ID. Used by streamLoop to re-check
// run state AFTER subscribing to the bus, closing the race where a run can
// finish between the handler's initial GetRun and the bus subscription.
type runGetter interface {
	GetRun(string) (*model.Run, error)
}

func lineEventFromBus(le events.LogLineEvent) LogLineSSEEvent {
	return LogLineSSEEvent{
		N:         le.LineNum,
		Ts:        le.Timestamp,
		Stream:    le.Stream,
		Text:      le.Text,
		Continued: le.Continued,
	}
}

func lineEventFromRecord(rec logutil.LogLineRecord) LogLineSSEEvent {
	return LogLineSSEEvent{
		N:      rec.LineNum,
		Stream: rec.Stream,
		Text:   rec.Text,
	}
}

// logStreamer drives a single SSE connection to a run's log. Lines are
// canonical: each event carries one absolute line number `n`. The streamer
// resolves the requested anchor (from / Last-Event-ID) against on-disk
// backfill, then transitions into a live phase fed by the event bus.
type logStreamer struct {
	logPath string
	send    sse.Sender

	lastSent int64
}

func newLogStreamer(send sse.Sender, logPath string) *logStreamer {
	return &logStreamer{
		logPath:  logPath,
		send:     send,
		lastSent: -1,
	}
}

// emitLine writes one line event and advances lastSent. Lines whose number
// is <= lastSent are silently skipped (dedupe across backfill ↔ live).
func (s *logStreamer) emitLine(line LogLineSSEEvent) error {
	if line.N <= s.lastSent {
		return nil
	}
	// safe: int == int64 on supported 64-bit platforms (linux/macOS/WSL on amd64/arm64).
	if err := s.send(sse.Message{ID: int(line.N), Data: line}); err != nil {
		return err
	}
	s.lastSent = line.N
	return nil
}

func (s *logStreamer) emitDropped(after int64, count int64) error {
	return s.send(sse.Message{Data: LogDroppedEvent{After: after, Count: count}})
}

func (s *logStreamer) emitDone(finalLine int64, status string) error {
	return s.send(sse.Message{Data: LogDoneEvent{FinalLine: finalLine, Status: status}})
}

func (s *logStreamer) emitRotated(firstAvailable int64) error {
	return s.send(sse.Message{Data: LogRotatedEvent{FirstAvailable: firstAvailable}})
}

// resolveBackfillAnchor turns the user-requested `from` into a concrete
// starting line number. Negative values mean "tail from end". The result is
// clamped against the available rotation window.
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

// streamLoop drives the SSE connection through three phases:
//  1. subscribe to the bus, draining published lines into a bounded buffer;
//  2. backfill from disk starting at the resolved anchor;
//  3. drain the buffer + follow live events until the run reaches a terminal
//     state.
//
// db lets us re-check run state after subscribing to close the race where a
// run completes between the caller's GetRun and Subscribe — without it we'd
// hang on the bus until context timeout. Tests pass nil to skip the recheck.
func (s *logStreamer) streamLoop(ctx context.Context, runID string, bus events.EventBus, db runGetter, anchorFrom, replayLimit int64, runEnded bool) {
	pendingCh := make(chan events.LogLineEvent, pendingBufferLimit)
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
			// Bounded buffer overflowed — drop the oldest queued event so we
			// keep up with the live tail, then enqueue the newest.
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

	// Backfill from disk so subscribers see history that was published before
	// they attached, plus everything between the anchor and the live cursor.
	backfill, firstAvailable, totalLines, err := logutil.ReadLineRange(s.logPath, anchorFrom, replayLimit)
	if err != nil {
		slog.Warn("SSE: backfill read failed", "runID", runID, "err", err)
	}
	resolvedAnchor := resolveBackfillAnchor(anchorFrom, replayLimit, totalLines, firstAvailable)
	if firstAvailable > 0 {
		if err := s.emitRotated(firstAvailable); err != nil {
			return
		}
	}
	for _, rec := range backfill {
		if rec.LineNum < resolvedAnchor {
			continue
		}
		if err := s.emitLine(lineEventFromRecord(rec)); err != nil {
			return
		}
	}

	// Race recheck: if the caller saw a non-terminal status but the run
	// completed before our Subscribe attached, the terminal event fired into
	// the void. db reads finalize that case so we don't hang.
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
				if err := s.emitLine(lineEventFromBus(le)); err != nil {
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
			_ = s.emitDropped(dropAfter, dropCount)
		}
		_ = s.emitDone(s.lastSent, "ended")
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
				_ = s.emitDropped(dropAfter, dropCount)
			}
			_ = s.emitDone(s.lastSent, "ended")
			return
		case le := <-pendingCh:
			if err := s.emitLine(lineEventFromBus(le)); err != nil {
				return
			}
			if dropCount > 0 {
				if err := s.emitDropped(dropAfter, dropCount); err != nil {
					return
				}
				dropCount = 0
				dropAfter = -1
			}
		}
	}
}
