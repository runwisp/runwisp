// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package logstream drives a single SSE connection to a run's log. It
// resolves the requested anchor (`from` / `Last-Event-ID`) against
// on-disk backfill, then transitions into a live phase fed by the event
// bus. The parent server package owns route registration and HTTP shape;
// logstream owns the streaming state machine.
package logstream

import (
	"context"
	"sync"

	"log/slog"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// PendingBufferLimit is the max LogLineEvent payloads buffered between
// the bus subscription and the disk-backfill drain. Once exceeded the
// streamer synthesizes a `dropped` event and evicts from the head — the
// live tail stays correct while old in-flight bursts are sacrificed first.
const PendingBufferLimit = 4096

// RegionBufferLimit bounds buffered live-region snapshots. Each snapshot is a
// full frame that supersedes the previous one, so on overflow we silently drop
// the oldest without a `dropped` notice — a missed intermediate frame self-heals
// on the next one and must never evict a committed line.
const RegionBufferLimit = 256

// LineEvent is the per-line payload emitted on the SSE wire as the `line`
// event. It matches LogLineEntry's shape; the parent server package owns
// the named-type alias that huma/sse uses for event-name dispatch.
type LineEvent struct {
	N          int64  `json:"n" doc:"Absolute line number"`
	Ts         int64  `json:"ts" doc:"Unix milliseconds timestamp; 0 if unavailable"`
	Stream     string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text       string `json:"text" doc:"Line content without trailing newline"`
	Continued  bool   `json:"continued,omitempty" doc:"True if this segment continues an oversized split line"`
	FrameCount int    `json:"frameCount,omitempty" doc:"Number of recorded prior frames if this line is a settled progress bar / redraw anchor; 0 otherwise"`
}

// RegionEvent is a live snapshot of a still-animating output region (a `\r`
// progress bar or multi-line ANSI redraw). It carries no line number: clients
// hold it in a transient overlay keyed by (Stream, Epoch), replacing the whole
// frame on each event, and never persist it. Empty Rows clears the overlay.
type RegionEvent struct {
	Stream string   `json:"stream" doc:"Stream identifier (stdout/stderr)"`
	Epoch  int      `json:"epoch" doc:"Region generation; bumps on screen reset so stale frames can be discarded"`
	Rows   []string `json:"rows" doc:"Current frame of the region, one entry per row; empty clears the overlay"`
}

// RotatedEvent fires when rotation has dropped lines below FirstAvailable.
type RotatedEvent struct {
	FirstAvailable int64 `json:"firstAvailable" doc:"Lowest line number still on disk after rotation"`
}

// DroppedEvent fires when the bounded pending buffer overflowed.
type DroppedEvent struct {
	After int64 `json:"after" doc:"Highest line number observed before drops occurred"`
	Count int64 `json:"count" doc:"Number of line events dropped due to overflow"`
}

// DoneEvent is the final event of a stream. FinalLine is the last line
// emitted; Status echoes the reason ("ended").
type DoneEvent struct {
	FinalLine int64  `json:"finalLine" doc:"Last line number emitted before the run terminated"`
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
	SendRegion(RegionEvent) error
	SendRotated(RotatedEvent) error
	SendDropped(DroppedEvent) error
	SendDone(DoneEvent) error
}

// RunGetter abstracts fetching a run by ID. Used by Run to re-check run
// state AFTER subscribing to the bus, closing the race where a run can
// finish between the handler's initial GetRun and the bus subscription.
type RunGetter interface {
	GetRun(context.Context, string) (*model.Run, error)
}

// StreamConfig holds the non-context parameters for Run.
type StreamConfig struct {
	Send        Sender
	LogPath     string
	RunID       string
	Bus         events.EventBus
	DB          RunGetter // nil skips the post-subscribe run-state recheck
	AnchorFrom  int64
	ReplayLimit int64
	RunEnded    bool
}

// Run drives the SSE connection through three phases:
//  1. subscribe to the bus, draining published lines into a bounded buffer;
//  2. backfill from disk starting at the resolved anchor;
//  3. drain the buffer + follow live events until the run reaches a
//     terminal state.
func Run(ctx context.Context, cfg StreamConfig) {
	s := newStreamer(cfg.Send, cfg.LogPath)
	s.streamLoop(ctx, cfg.RunID, cfg.Bus, cfg.DB, cfg.AnchorFrom, cfg.ReplayLimit, cfg.RunEnded)
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
	Region  func(RegionEvent) any
	Rotated func(RotatedEvent) any
	Dropped func(DroppedEvent) any
	Done    func(DoneEvent) any
}

func (h HumaSender) SendLine(e LineEvent) error {
	return h.Inner(sse.Message{ID: int(e.N), Data: h.Line(e)})
}

// SendRegion emits a region snapshot WITHOUT an SSE id, so it never becomes a
// Last-Event-ID resume cursor — reconnect resumes from the last committed line
// and the next live frame repaints the overlay.
func (h HumaSender) SendRegion(e RegionEvent) error {
	return h.Inner(sse.Message{Data: h.Region(e)})
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
		N:          le.LineNum,
		Ts:         le.Timestamp,
		Stream:     le.Stream,
		Text:       le.Text,
		Continued:  le.Continued,
		FrameCount: le.FrameCount,
	}
}

func regionFromBus(re events.LogRegionEvent) RegionEvent {
	return RegionEvent{
		Stream: re.Stream,
		Epoch:  re.Epoch,
		Rows:   re.Rows,
	}
}

func lineFromRecord(rec logutil.LogLineRecord, frameCount int) LineEvent {
	return LineEvent{
		N:          rec.LineNum,
		Stream:     rec.Stream,
		Text:       rec.Text,
		FrameCount: frameCount,
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

// streamDropTracker holds the mutable drop-accounting state shared between
// the bus subscription handler (publisher goroutine) and the SSE loop. The
// bus invokes handlers synchronously on the publisher's goroutine, so the
// writer and reader run concurrently — every field access goes through mu.
type streamDropTracker struct {
	mu    sync.Mutex
	after int64 // highest dropped line number (-1 = none)
	count int64 // total dropped since last flush
}

// recordDrop accounts for one dropped line at lineNum.
func (dt *streamDropTracker) recordDrop(lineNum int64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.count++
	if lineNum > dt.after {
		dt.after = lineNum
	}
}

// snapshot returns the current drop accounting without resetting it.
func (dt *streamDropTracker) snapshot() (after, count int64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	return dt.after, dt.count
}

// commitSent subtracts count already-reported drops, resetting the anchor once
// the backlog is drained. Any drops recorded after the matching snapshot are
// preserved for the next flush.
func (dt *streamDropTracker) commitSent(count int64) {
	dt.mu.Lock()
	defer dt.mu.Unlock()
	dt.count -= count
	if dt.count <= 0 {
		dt.count = 0
		dt.after = -1
	}
}

func (s *streamer) streamLoop(ctx context.Context, runID string, bus events.EventBus, db RunGetter, anchorFrom, replayLimit int64, runEnded bool) {
	pendingCh := make(chan events.LogLineEvent, PendingBufferLimit)
	regionCh := make(chan events.LogRegionEvent, RegionBufferLimit)
	terminalCh := make(chan struct{}, 1)
	dt := &streamDropTracker{after: -1}

	// Subscribe before reading disk so no live events are missed.
	unsubLine := bus.Subscribe(events.EventLogLine, makeLineHandler(runID, pendingCh, dt))
	defer unsubLine()
	unsubRegion := bus.Subscribe(events.EventLogRegion, makeRegionHandler(runID, regionCh))
	defer unsubRegion()
	termHandler := makeTerminalHandler(runID, terminalCh)
	unsubCompleted := bus.Subscribe(events.EventRunCompleted, termHandler)
	defer unsubCompleted()
	unsubFailed := bus.Subscribe(events.EventRunFailed, termHandler)
	defer unsubFailed()

	// Backfill from disk so subscribers see history published before they
	// attached, plus everything between the anchor and the live cursor.
	backfill, firstAvailable, totalLines, err := logutil.ReadLineRange(s.logPath, anchorFrom, replayLimit)
	if err != nil {
		slog.Warn("SSE: backfill read failed", "runID", runID, "err", err)
	}
	resolvedAnchor := resolveBackfillAnchor(anchorFrom, replayLimit, totalLines, firstAvailable)
	// Stamp frame-history availability onto backfilled anchor lines so a
	// finished run loaded fresh (not just the live path) keeps its rewind
	// affordance. Read once per connection, mirroring humaGetLogPage.
	frameCounts := logutil.ReadFrameHistoryCounts(s.logPath)
	if err := s.emitBackfill(backfill, resolvedAnchor, firstAvailable, frameCounts); err != nil {
		return
	}

	// Race recheck: if the caller saw a non-terminal status but the run
	// completed before our Subscribe attached, the terminal event fired
	// into the void. db reads finalize that case so we don't hang.
	terminal := runEnded
	if !terminal && db != nil {
		if r, getErr := db.GetRun(ctx, runID); getErr == nil && r != nil && r.Status == model.PhaseEnded {
			terminal = true
		}
	}

	if terminal {
		s.sendTerminalEvents(pendingCh, dt)
		return
	}
	s.runLiveLoop(ctx, pendingCh, regionCh, terminalCh, dt)
}

// makeLineHandler returns the EventLogLine subscription handler for runID.
func makeLineHandler(runID string, pendingCh chan events.LogLineEvent, dt *streamDropTracker) func(events.Event) {
	return func(e events.Event) {
		le, ok := e.Data.(events.LogLineEvent)
		if !ok || le.RunID != runID {
			return
		}
		bufferLogLine(le, pendingCh, dt)
	}
}

// makeRegionHandler returns the EventLogRegion subscription handler for runID.
func makeRegionHandler(runID string, regionCh chan events.LogRegionEvent) func(events.Event) {
	return func(e events.Event) {
		re, ok := e.Data.(events.LogRegionEvent)
		if !ok || re.RunID != runID {
			return
		}
		bufferRegion(re, regionCh)
	}
}

// bufferRegion enqueues re, evicting the oldest snapshot on overflow. Frames are
// full-state and self-superseding, so a dropped frame needs no accounting.
func bufferRegion(re events.LogRegionEvent, regionCh chan events.LogRegionEvent) {
	select {
	case regionCh <- re:
	default:
		select {
		case <-regionCh:
		default:
		}
		select {
		case regionCh <- re:
		default:
		}
	}
}

// makeTerminalHandler returns the run-terminal subscription handler for runID.
func makeTerminalHandler(runID string, terminalCh chan struct{}) func(events.Event) {
	return func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.Run == nil || re.Run.ID != runID {
			return
		}
		signalTerminalCh(terminalCh)
	}
}

// signalTerminalCh sends a non-blocking signal to terminalCh.
func signalTerminalCh(terminalCh chan struct{}) {
	select {
	case terminalCh <- struct{}{}:
	default:
	}
}

// bufferLogLine enqueues le in pendingCh. On overflow it evicts the oldest
// event and records the drop range in dt before re-enqueueing the newest.
func bufferLogLine(le events.LogLineEvent, pendingCh chan events.LogLineEvent, dt *streamDropTracker) {
	select {
	case pendingCh <- le:
	default:
		// Bounded buffer overflowed — evict the oldest so the live tail
		// stays current, then try to enqueue the newest.
		select {
		case dropped := <-pendingCh:
			dt.recordDrop(dropped.LineNum)
		default:
		}
		select {
		case pendingCh <- le:
		default:
			dt.recordDrop(le.LineNum)
		}
	}
}

// emitBackfill sends the rotated notice (if any) and replays backfill lines
// starting at resolvedAnchor. Returns the first send error encountered.
func (s *streamer) emitBackfill(backfill []logutil.LogLineRecord, resolvedAnchor, firstAvailable int64, frameCounts map[int64]int) error {
	if firstAvailable > 0 {
		if err := s.send.SendRotated(RotatedEvent{FirstAvailable: firstAvailable}); err != nil {
			return err
		}
	}
	for _, rec := range backfill {
		if rec.LineNum < resolvedAnchor {
			continue
		}
		if err := s.emitLine(lineFromRecord(rec, frameCounts[rec.LineNum])); err != nil {
			return err
		}
	}
	return nil
}

// drainPendingCh reads all buffered LogLineEvents from pendingCh without
// blocking, emitting each to the SSE stream. Returns on the first send error.
func (s *streamer) drainPendingCh(pendingCh <-chan events.LogLineEvent) error {
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

// sendTerminalEvents drains pendingCh, flushes any recorded drops, and sends
// the final done event.
func (s *streamer) sendTerminalEvents(pendingCh <-chan events.LogLineEvent, dt *streamDropTracker) {
	if err := s.drainPendingCh(pendingCh); err != nil {
		return
	}
	if after, count := dt.snapshot(); count > 0 {
		_ = s.send.SendDropped(DroppedEvent{After: after, Count: count})
	}
	_ = s.send.SendDone(DoneEvent{FinalLine: s.lastSent, Status: "ended"})
}

// handleLiveLine emits one live log line and flushes any queued drop notice.
// Returns false if a send error occurred (caller should stop the loop).
func (s *streamer) handleLiveLine(le events.LogLineEvent, dt *streamDropTracker) bool {
	if err := s.emitLine(lineFromBus(le)); err != nil {
		return false
	}
	if after, count := dt.snapshot(); count > 0 {
		if err := s.send.SendDropped(DroppedEvent{After: after, Count: count}); err != nil {
			return false
		}
		dt.commitSent(count)
	}
	return true
}

// runLiveLoop drives the SSE connection until ctx is cancelled, the run ends
// (terminalCh fires), or a send error occurs.
func (s *streamer) runLiveLoop(ctx context.Context, pendingCh <-chan events.LogLineEvent, regionCh <-chan events.LogRegionEvent, terminalCh <-chan struct{}, dt *streamDropTracker) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-terminalCh:
			s.sendTerminalEvents(pendingCh, dt)
			return
		case le := <-pendingCh:
			if !s.handleLiveLine(le, dt) {
				return
			}
		case re := <-regionCh:
			if err := s.send.SendRegion(regionFromBus(re)); err != nil {
				return
			}
		}
	}
}
