// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingFailureSink struct {
	mu       sync.Mutex
	captured []*Event
}

func (s *recordingFailureSink) IngestSynthetic(ev *Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = append(s.captured, ev)
}

func (s *recordingFailureSink) Captured() []*Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Event, len(s.captured))
	copy(out, s.captured)
	return out
}

type executeChannel struct {
	id     string
	execFn func(context.Context, *Event) error
	hits   atomic.Int64
}

func (a *executeChannel) ID() string { return a.id }
func (a *executeChannel) Execute(ctx context.Context, ev *Event) error {
	a.hits.Add(1)
	if a.execFn != nil {
		return a.execFn(ctx, ev)
	}
	return nil
}
func (a *executeChannel) Close(context.Context) error { return nil }

func TestDispatcher_DeliversMatchingActions(t *testing.T) {
	var (
		mu      sync.Mutex
		seen    []*Event
		channel = &executeChannel{id: "a"}
	)
	channel.execFn = func(_ context.Context, ev *Event) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, ev)
		return nil
	}
	channels := map[string]Channel{channel.id: channel}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"a"}}}, channels)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, channels, 8, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError, TaskName: "first"})
	d.dispatch(&Event{Kind: KindRunSucceeded, Severity: SevInfo, TaskName: "second"})

	require.Eventually(t, func() bool { return channel.hits.Load() == 2 }, time.Second, 10*time.Millisecond)
	d.closeQueues()
	d.waitWorkers()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seen, 2)
	assert.Equal(t, KindRunFailed, seen[0].Kind)
	assert.Equal(t, "first", seen[0].TaskName)
	assert.Equal(t, KindRunSucceeded, seen[1].Kind)
	assert.Equal(t, "second", seen[1].TaskName)
	assert.Empty(t, sink.Captured(), "successful deliveries must not surface a failure")
}

func TestDispatcher_PermanentFailureSurfacedToSink(t *testing.T) {
	cause := errors.New("503 Service Unavailable (after retries)")
	a := &executeChannel{
		id:     "slack:ops",
		execFn: func(context.Context, *Event) error { return cause },
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slack:ops"}}}, channels)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, channels, 8, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError, TaskName: "backup-db"})
	require.Eventually(t, func() bool { return len(sink.Captured()) == 1 }, time.Second, 10*time.Millisecond)

	got := sink.Captured()[0]
	assert.Equal(t, KindNotifyDeliveryFailed, got.Kind)
	assert.Equal(t, SevWarn, got.Severity)
	assert.Equal(t, "backup-db", got.TaskName)
	assert.Equal(t, cause.Error(), got.Reason)
	assert.Equal(t, "slack:ops", got.Extra["channel"])
	assert.Equal(t, "run.failed", got.Extra["original_kind"])

	d.closeQueues()
	d.waitWorkers()
}

func TestDispatcher_ContextCancelDoesNotSurfaceFailure(t *testing.T) {
	released := make(chan struct{})
	a := &executeChannel{
		id: "slow",
		execFn: func(ctx context.Context, _ *Event) error {
			<-ctx.Done()
			close(released)
			return ctx.Err()
		},
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slow"}}}, channels)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, channels, 4, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	d.startWorkers(ctx)
	d.dispatch(&Event{Kind: KindRunFailed})
	require.Eventually(t, func() bool { return a.hits.Load() == 1 }, time.Second, 10*time.Millisecond)
	cancel()
	<-released
	d.closeQueues()
	d.waitWorkers()
	assert.Empty(t, sink.Captured(), "ctx cancel must not be surfaced as a delivery failure")
}

// TestDispatcher_RedactErrorPreservesCancelDetection is the regression test
// for the RedactError bug: channels used to build their returned error with
// fmt.Errorf("%s: %s", ..., Redact(err.Error(), secret)), which stringifies
// the cause and breaks the Unwrap() chain. errors.Is(err, context.Canceled)
// then could never see through to the real cause, so a channel interrupted by
// shutdown looked like a permanent failure. RedactError wraps with %w so the
// chain survives redaction.
func TestDispatcher_RedactErrorPreservesCancelDetection(t *testing.T) {
	wrapped := RedactError(context.Canceled, "super-secret-token")
	require.True(t, errors.Is(wrapped, context.Canceled),
		"RedactError must preserve Unwrap() so errors.Is still finds context.Canceled")

	released := make(chan struct{})
	a := &executeChannel{
		id: "slack:ops",
		execFn: func(ctx context.Context, _ *Event) error {
			<-ctx.Done()
			close(released)
			return RedactError(ctx.Err(), "super-secret-token")
		},
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slack:ops"}}}, channels)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, channels, 4, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	d.startWorkers(ctx)
	d.dispatch(&Event{Kind: KindRunFailed})
	require.Eventually(t, func() bool { return a.hits.Load() == 1 }, time.Second, 10*time.Millisecond)
	cancel()
	<-released
	d.closeQueues()
	d.waitWorkers()
	assert.Empty(t, sink.Captured(),
		"a RedactError-wrapped context.Canceled must still be recognized as shutdown, not surfaced as a delivery failure")
}

func TestDispatcher_DropsOldestWhenQueueFull(t *testing.T) {
	release := make(chan struct{})
	startedFirst := make(chan struct{})
	var firstOnce sync.Once
	var seen []string
	var seenMu sync.Mutex
	a := &executeChannel{
		id: "slow",
		execFn: func(ctx context.Context, ev *Event) error {
			firstOnce.Do(func() { close(startedFirst) })
			<-release
			seenMu.Lock()
			seen = append(seen, ev.TaskName)
			seenMu.Unlock()
			return nil
		},
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slow"}}}, channels)
	sink := &recordingFailureSink{}
	// Capacity 1: with the first event held by the worker plus one queued,
	// any further dispatch must evict the oldest queued event.
	d := newDispatcher(router, channels, 1, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, TaskName: "first"})
	<-startedFirst
	for i := 0; i < 10; i++ {
		d.dispatch(&Event{Kind: KindRunFailed, TaskName: "burst"})
	}
	d.dispatch(&Event{Kind: KindRunFailed, TaskName: "newest"})
	close(release)
	d.closeQueues()
	d.waitWorkers()

	assert.Greater(t, d.DroppedActionCount(), uint64(0), "must record drops under pressure")
	seenMu.Lock()
	defer seenMu.Unlock()
	require.NotEmpty(t, seen, "worker must have processed at least one event")
	assert.Equal(t, "newest", seen[len(seen)-1], "newest event must reach the worker last")
}

// TestNewDispatcher_QueueSizeFallback proves the zero-or-negative queueSize
// branch applies the 256-slot default by dispatching many events at once
// while the worker is held still — if the fallback failed, the queue size
// would be 0 and even the first event would drop.
func TestNewDispatcher_QueueSizeFallback(t *testing.T) {
	release := make(chan struct{})
	var processed int64
	var mu sync.Mutex
	a := &executeChannel{
		id: "a",
		execFn: func(_ context.Context, _ *Event) error {
			<-release
			mu.Lock()
			processed++
			mu.Unlock()
			return nil
		},
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"a"}}}, channels)

	// queueSize=0 must fall back to 256; nil logger must fall back to slog.Default.
	d := newDispatcher(router, channels, 0, RealClock(), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	// Queue 200 events while the worker is blocked. A size-0 queue would
	// have already dropped events; size-256 (the fallback) accepts all 200.
	for i := 0; i < 200; i++ {
		d.dispatch(&Event{Kind: KindRunFailed, TaskName: "burst"})
	}
	close(release)
	d.closeQueues()
	d.waitWorkers()

	assert.Equal(t, uint64(0), d.DroppedActionCount(),
		"with queueSize fallback to 256, 200 buffered events must not drop")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, int64(200), processed, "all events must process after worker resumes")
}

func TestDispatcher_DispatchNilNoOp(t *testing.T) {
	a := &executeChannel{id: "a"}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"a"}}}, channels)
	d := newDispatcher(router, channels, 8, RealClock(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)
	d.dispatch(nil) // nil → no-op, must not panic
	d.closeQueues()
	d.waitWorkers()
	assert.Equal(t, int64(0), a.hits.Load(), "nil dispatch must not reach worker")
}

func TestDispatcher_ExecuteOneWithNilFailures_LogsOnly(t *testing.T) {
	cause := errors.New("permanent")
	a := &executeChannel{
		id:     "noSink",
		execFn: func(context.Context, *Event) error { return cause },
	}
	channels := map[string]Channel{a.id: a}
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"noSink"}}}, channels)
	// nil failures sink: error must be logged, not forwarded
	d := newDispatcher(router, channels, 8, RealClock(), nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)
	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError})
	require.Eventually(t, func() bool { return a.hits.Load() >= 1 }, time.Second, 10*time.Millisecond)
	d.closeQueues()
	d.waitWorkers()
	// No panic, no failure sink to check — just verify the worker ran
	assert.GreaterOrEqual(t, a.hits.Load(), int64(1))
}

// TestDispatcher_CycleGuard_DeliveryFailedDoesNotReRoute asserts the cycle
// guard: when a channel permanently fails and produces a synthetic
// KindNotifyDeliveryFailed event, that synthetic must NOT re-enter the
// dispatch path — even though a MatchAll rule would otherwise match it.
// The guard is structural (reportDeliveryFailure → sink.IngestSynthetic,
// bypassing the router) — this test pins it down with an explicit assertion.
func TestDispatcher_CycleGuard_DeliveryFailedDoesNotReRoute(t *testing.T) {
	cause := errors.New("permanent")
	var (
		seenMu sync.Mutex
		seen   []Kind
	)
	a := &executeChannel{
		id: "slack:ops",
		execFn: func(_ context.Context, ev *Event) error {
			seenMu.Lock()
			seen = append(seen, ev.Kind)
			seenMu.Unlock()
			return cause
		},
	}
	channels := map[string]Channel{a.id: a}
	// MatchAll would happily route KindNotifyDeliveryFailed back to "slack:ops"
	// if the synthetic event ever re-entered dispatch. It must not.
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"slack:ops"}}}, channels)
	sink := &recordingFailureSink{}
	d := newDispatcher(router, channels, 8, RealClock(), sink, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)

	d.dispatch(&Event{Kind: KindRunFailed, Severity: SevError, TaskName: "backup-db"})

	// Wait for the synthetic to surface to the sink (proves the channel
	// failed and the failure path ran).
	require.Eventually(t, func() bool { return len(sink.Captured()) == 1 }, time.Second, 10*time.Millisecond)

	d.closeQueues()
	d.waitWorkers()

	syn := sink.Captured()[0]
	assert.Equal(t, KindNotifyDeliveryFailed, syn.Kind)
	assert.Equal(t, "slack:ops", syn.Extra["channel"])

	// The channel never saw the synthetic. Every Execute call must have been
	// for the original Kind — KindNotifyDeliveryFailed must NOT appear.
	seenMu.Lock()
	defer seenMu.Unlock()
	require.NotEmpty(t, seen, "channel must have been invoked at least once for the original event")
	for _, k := range seen {
		assert.NotEqual(t, KindNotifyDeliveryFailed, k,
			"cycle guard violated: synthetic delivery-failed event re-entered dispatch")
	}
}

func TestDispatcher_UnknownActionID_Skipped(t *testing.T) {
	// Router returns an action ("ghost") not present in the channels map.
	// The dispatch must silently skip it.
	ghost := &executeChannel{id: "ghost"}
	channels := map[string]Channel{} // intentionally empty
	router := NewRouter([]Rule{{Match: MatchAll(), ActionIDs: []string{"ghost"}}}, channels)
	d := newDispatcher(router, channels, 8, RealClock(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.startWorkers(ctx)
	d.dispatch(&Event{Kind: KindRunFailed})
	d.closeQueues()
	d.waitWorkers()
	assert.Equal(t, int64(0), ghost.hits.Load(), "unknown action must be skipped")
}
