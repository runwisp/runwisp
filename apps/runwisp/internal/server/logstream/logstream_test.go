// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logstream

import (
	"errors"
	"testing"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSender records all calls for assertions.
type mockSender struct {
	lines   []LineEvent
	regions []RegionEvent
	rotated []RotatedEvent
	dropped []DroppedEvent
	done    []DoneEvent
	sendErr error
}

func (m *mockSender) SendLine(e LineEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.lines = append(m.lines, e)
	return nil
}

func (m *mockSender) SendRegion(e RegionEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.regions = append(m.regions, e)
	return nil
}

func (m *mockSender) SendRotated(e RotatedEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.rotated = append(m.rotated, e)
	return nil
}

func (m *mockSender) SendDropped(e DroppedEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.dropped = append(m.dropped, e)
	return nil
}

func (m *mockSender) SendDone(e DoneEvent) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.done = append(m.done, e)
	return nil
}

// --- HumaSender tests ---

func TestHumaSender_SendRotated(t *testing.T) {
	var got sse.Message
	hs := HumaSender{
		Inner:   func(msg sse.Message) error { got = msg; return nil },
		Rotated: func(e RotatedEvent) any { return e },
	}
	require.NoError(t, hs.SendRotated(RotatedEvent{FirstAvailable: 42}))
	assert.Equal(t, RotatedEvent{FirstAvailable: 42}, got.Data)
}

func TestHumaSender_SendDropped(t *testing.T) {
	var got sse.Message
	hs := HumaSender{
		Inner:   func(msg sse.Message) error { got = msg; return nil },
		Dropped: func(e DroppedEvent) any { return e },
	}
	require.NoError(t, hs.SendDropped(DroppedEvent{After: 5, Count: 3}))
	assert.Equal(t, DroppedEvent{After: 5, Count: 3}, got.Data)
}

// --- emitLine ---

func TestEmitLine_SkipsDuplicate(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")
	s.lastSent = 5

	require.NoError(t, s.emitLine(LineEvent{N: 5, Text: "dup"}))
	assert.Empty(t, m.lines)
	assert.Equal(t, int64(5), s.lastSent) // unchanged
}

func TestEmitLine_AdvancesLastSent(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")

	require.NoError(t, s.emitLine(LineEvent{N: 3, Text: "hello"}))
	assert.Equal(t, int64(3), s.lastSent)
	assert.Len(t, m.lines, 1)
}

func TestEmitLine_PropagatesError(t *testing.T) {
	m := &mockSender{sendErr: errors.New("conn closed")}
	s := newStreamer(m, "/tmp/test.log")
	require.Error(t, s.emitLine(LineEvent{N: 1, Text: "x"}))
}

// --- resolveBackfillAnchor ---

func TestResolveBackfillAnchor_NegativeFrom(t *testing.T) {
	// from=-50, total=100 → anchor=50
	assert.Equal(t, int64(50), resolveBackfillAnchor(-50, 0, 100, 0))
}

func TestResolveBackfillAnchor_NegativeFromClampedToFirstAvailable(t *testing.T) {
	// from=-10, total=100, firstAvailable=95 → anchor=95
	assert.Equal(t, int64(95), resolveBackfillAnchor(-10, 0, 100, 95))
}

func TestResolveBackfillAnchor_NegativeFromNeverBelowZero(t *testing.T) {
	// from=-200, total=50, firstAvailable=0 → anchor=0
	assert.Equal(t, int64(0), resolveBackfillAnchor(-200, 0, 50, 0))
}

func TestResolveBackfillAnchor_FromBelowFirstAvailable(t *testing.T) {
	// from=5, firstAvailable=20 → clamped to 20
	assert.Equal(t, int64(20), resolveBackfillAnchor(5, 0, 100, 20))
}

func TestResolveBackfillAnchor_ReplayLimitExceeded(t *testing.T) {
	// from=0, replayLimit=10, total=100 → from=90
	assert.Equal(t, int64(90), resolveBackfillAnchor(0, 10, 100, 0))
}

func TestResolveBackfillAnchor_ReplayLimitClampedToFirstAvailable(t *testing.T) {
	// from=0, replayLimit=10, total=100, firstAvailable=95 → from=95
	assert.Equal(t, int64(95), resolveBackfillAnchor(0, 10, 100, 95))
}

func TestResolveBackfillAnchor_NoReplayLimit(t *testing.T) {
	// from=10, replayLimit=0 → no change
	assert.Equal(t, int64(10), resolveBackfillAnchor(10, 0, 100, 0))
}

// --- bufferLogLine ---

func makeLogLineEvent(lineNum int64) events.LogLineEvent {
	return events.LogLineEvent{RunID: "r1", LineNum: lineNum, Text: "x"}
}

func TestBufferLogLine_NormalEnqueue(t *testing.T) {
	ch := make(chan events.LogLineEvent, 5)
	dt := &streamDropTracker{after: -1}
	bufferLogLine(makeLogLineEvent(1), ch, dt)
	assert.Len(t, ch, 1)
	assert.Equal(t, int64(0), dt.count)
}

func TestBufferLogLine_OverflowEvictsOldest(t *testing.T) {
	ch := make(chan events.LogLineEvent, 2)
	dt := &streamDropTracker{after: -1}
	bufferLogLine(makeLogLineEvent(1), ch, dt)
	bufferLogLine(makeLogLineEvent(2), ch, dt)

	// Third event overflows → evicts line 1, enqueues line 3.
	bufferLogLine(makeLogLineEvent(3), ch, dt)

	assert.Equal(t, int64(1), dt.count)
	assert.Equal(t, int64(1), dt.after)
	assert.Len(t, ch, 2)
}

// TestBackpressure_SlowConsumerDropsExcess exercises the end-to-end
// backpressure path: a real event bus, the real makeLineHandler
// subscription, and a never-draining pendingCh. After a burst that
// exceeds the buffer, dt.count must equal the number of dropped lines
// and dt.after must point to the highest-numbered line ever evicted.
// This validates the invariant "slow SSE consumer must not block task
// execution — excess events drop, never block."
func TestBackpressure_SlowConsumerDropsExcess(t *testing.T) {
	const (
		runID    = "r1"
		capacity = 4
		burst    = 100
	)
	bus := events.NewEventBus()

	// Never-draining pending channel — the "slow consumer" stand-in.
	pendingCh := make(chan events.LogLineEvent, capacity)
	dt := &streamDropTracker{after: -1}

	unsub := bus.Subscribe(events.EventLogLine, makeLineHandler(runID, pendingCh, dt))
	defer unsub()

	// Publish more events than the channel can hold. Each publish is
	// synchronous from the bus to bufferLogLine — overflow is handled
	// by evicting from the head of pendingCh, never by blocking.
	for i := int64(1); i <= burst; i++ {
		bus.Publish(events.EventLogLine, events.LogLineEvent{RunID: runID, LineNum: i, Text: "x"})
	}

	// Capacity slots survive; the rest are accounted for as drops.
	assert.Equal(t, capacity, len(pendingCh),
		"pending channel must remain at exactly its capacity after a burst")
	assert.Equal(t, int64(burst-capacity), dt.count,
		"dropped count must equal the excess over capacity")
	// `after` is the highest line *evicted*. With the channel holding the
	// last `capacity` lines (97..100), evictions cover 1..96 — so after=96.
	assert.Equal(t, int64(burst-capacity), dt.after,
		"after-cursor must point to the highest evicted line number")
}

// TestBackpressure_OtherRunIDsAreIgnored confirms the per-run filter on
// the bus handler: events for a different run ID must not consume buffer
// slots or increment the drop counter on this stream.
func TestBackpressure_OtherRunIDsAreIgnored(t *testing.T) {
	bus := events.NewEventBus()
	pendingCh := make(chan events.LogLineEvent, 2)
	dt := &streamDropTracker{after: -1}

	unsub := bus.Subscribe(events.EventLogLine, makeLineHandler("mine", pendingCh, dt))
	defer unsub()

	for i := int64(1); i <= 10; i++ {
		bus.Publish(events.EventLogLine, events.LogLineEvent{RunID: "other", LineNum: i, Text: "x"})
	}

	assert.Empty(t, pendingCh, "events for other runs must not enter this stream's buffer")
	assert.Equal(t, int64(0), dt.count, "drops must not be charged for foreign-run events")
}

// --- emitBackfill ---

func TestEmitBackfill_WithRotatedNotice(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")

	// firstAvailable > 0 triggers a rotated notice.
	require.NoError(t, s.emitBackfill(nil, 5, 10))
	require.Len(t, m.rotated, 1)
	assert.Equal(t, int64(10), m.rotated[0].FirstAvailable)
}

func TestEmitBackfill_SendRotatedError(t *testing.T) {
	m := &mockSender{sendErr: errors.New("write error")}
	s := newStreamer(m, "/tmp/test.log")
	require.Error(t, s.emitBackfill(nil, 5, 10))
}

// --- sendTerminalEvents ---

func TestSendTerminalEvents_WithDrops(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")
	dt := &streamDropTracker{after: 7, count: 3}

	s.sendTerminalEvents(make(chan events.LogLineEvent, 10), dt)

	require.Len(t, m.dropped, 1)
	assert.Equal(t, int64(7), m.dropped[0].After)
	assert.Equal(t, int64(3), m.dropped[0].Count)
	require.Len(t, m.done, 1)
}

func TestSendTerminalEvents_NoDrops(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")
	dt := &streamDropTracker{after: -1, count: 0}

	s.sendTerminalEvents(make(chan events.LogLineEvent, 10), dt)

	assert.Empty(t, m.dropped)
	require.Len(t, m.done, 1)
}

// --- handleLiveLine ---

func TestHandleLiveLine_NoDrops(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")
	dt := &streamDropTracker{after: -1, count: 0}

	assert.True(t, s.handleLiveLine(events.LogLineEvent{LineNum: 1, Text: "hi"}, dt))
	assert.Empty(t, m.dropped)
}

func TestHandleLiveLine_FlushesDropNotice(t *testing.T) {
	m := &mockSender{}
	s := newStreamer(m, "/tmp/test.log")
	dt := &streamDropTracker{after: 5, count: 2}

	assert.True(t, s.handleLiveLine(events.LogLineEvent{LineNum: 6, Text: "after drop"}, dt))
	require.Len(t, m.dropped, 1)
	assert.Equal(t, int64(0), dt.count) // reset after flush
}

func TestHandleLiveLine_SendError(t *testing.T) {
	m := &mockSender{sendErr: errors.New("broken pipe")}
	s := newStreamer(m, "/tmp/test.log")
	dt := &streamDropTracker{after: -1, count: 0}

	assert.False(t, s.handleLiveLine(events.LogLineEvent{LineNum: 1}, dt))
}

func TestHandleLiveLine_DropFlushError(t *testing.T) {
	callCount := 0
	var captureSend mockSender
	// First call (line emit) succeeds; second call (drop flush) fails.
	customSender := &conditionalErrSender{
		onLine:  func(e LineEvent) error { callCount++; captureSend.lines = append(captureSend.lines, e); return nil },
		onDrop:  func(e DroppedEvent) error { return errors.New("drop send failed") },
		onOther: func() error { return nil },
	}
	s := newStreamer(customSender, "/tmp/test.log")
	dt := &streamDropTracker{after: 5, count: 2}

	ok := s.handleLiveLine(events.LogLineEvent{LineNum: 6}, dt)
	assert.False(t, ok) // drop flush failed → stop
}

// conditionalErrSender lets tests inject per-method errors.
type conditionalErrSender struct {
	onLine  func(LineEvent) error
	onDrop  func(DroppedEvent) error
	onOther func() error
}

func (c *conditionalErrSender) SendLine(e LineEvent) error       { return c.onLine(e) }
func (c *conditionalErrSender) SendRegion(RegionEvent) error     { return c.onOther() }
func (c *conditionalErrSender) SendRotated(RotatedEvent) error   { return c.onOther() }
func (c *conditionalErrSender) SendDropped(e DroppedEvent) error { return c.onDrop(e) }
func (c *conditionalErrSender) SendDone(DoneEvent) error         { return c.onOther() }

// --- makeLineHandler / makeTerminalHandler ---

func TestMakeLineHandler_WrongRunID(t *testing.T) {
	ch := make(chan events.LogLineEvent, 5)
	dt := &streamDropTracker{after: -1}
	handler := makeLineHandler("run-A", ch, dt)

	handler(events.Event{Data: events.LogLineEvent{RunID: "run-B", LineNum: 1}})
	assert.Empty(t, ch)
}

func TestMakeLineHandler_WrongDataType(t *testing.T) {
	ch := make(chan events.LogLineEvent, 5)
	dt := &streamDropTracker{after: -1}
	handler := makeLineHandler("run-A", ch, dt)

	handler(events.Event{Data: events.RunEvent{}})
	assert.Empty(t, ch)
}

func TestMakeTerminalHandler_WrongRunID(t *testing.T) {
	termCh := make(chan struct{}, 1)
	handler := makeTerminalHandler("run-A", termCh)

	run := &model.Run{ID: "run-B"}
	handler(events.Event{Data: events.RunEvent{Run: run}})
	assert.Empty(t, termCh)
}

func TestMakeTerminalHandler_WrongDataType(t *testing.T) {
	termCh := make(chan struct{}, 1)
	handler := makeTerminalHandler("run-A", termCh)

	handler(events.Event{Data: events.LogLineEvent{RunID: "run-A"}})
	assert.Empty(t, termCh)
}

func TestMakeTerminalHandler_NilRun(t *testing.T) {
	termCh := make(chan struct{}, 1)
	handler := makeTerminalHandler("run-A", termCh)

	handler(events.Event{Data: events.RunEvent{Run: nil}})
	assert.Empty(t, termCh)
}
