// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

func failedRunEvent() events.Event {
	reason := model.ReasonFailed
	run := &model.Run{
		ID:        "01HFAIL",
		TaskName:  "task-x",
		Status:    model.PhaseEnded,
		EndReason: &reason,
		ExitCode:  1,
	}
	return events.Event{
		Type:      events.EventRunFailed,
		Timestamp: time.Now(),
		Data:      events.RunEvent{Run: run},
	}
}

func missedRunEvent(taskName string) events.Event {
	reason := model.ReasonMissed
	run := &model.Run{
		ID:        "01HMISS",
		TaskName:  taskName,
		Status:    model.PhaseEnded,
		EndReason: &reason,
		ExitCode:  -1,
	}
	return events.Event{
		Type:      events.EventRunFailed,
		Timestamp: time.Now(),
		Data:      events.RunEvent{Run: run, Error: "2 scheduled runs missed (daemon was down)"},
	}
}

// TestOnBusEvent_MutedMissedTaskDropped verifies notify_on_missed = false drops
// the run.missed alert at ingress without counting it as a backpressure drop.
func TestOnBusEvent_MutedMissedTaskDropped(t *testing.T) {
	svc := New(Config{MutedMissedTasks: map[string]struct{}{"quiet-task": {}}})

	before := svc.droppedIngress.Load()
	svc.onBusEvent(missedRunEvent("quiet-task"))

	assert.Empty(t, svc.ingressCh, "muted task's run.missed must not reach routing")
	assert.Equal(t, before, svc.droppedIngress.Load(),
		"an intentional mute is not a backpressure drop")
}

// TestOnBusEvent_UnmutedMissedTaskRoutes verifies a task without the mute still
// delivers its run.missed event, and that the mute is strictly per-task.
func TestOnBusEvent_UnmutedMissedTaskRoutes(t *testing.T) {
	svc := New(Config{MutedMissedTasks: map[string]struct{}{"quiet-task": {}}})

	svc.onBusEvent(missedRunEvent("loud-task"))

	select {
	case ev := <-svc.ingressCh:
		assert.Equal(t, KindRunMissed, ev.Kind)
		assert.Equal(t, "loud-task", ev.TaskName)
	default:
		t.Fatal("expected the un-muted task's run.missed in ingressCh")
	}
}

// TestOnBusEvent_MissedRoutesByDefault confirms misses alert with no mute set —
// the default-on behaviour (decision: misses reach failure subscribers).
func TestOnBusEvent_MissedRoutesByDefault(t *testing.T) {
	svc := New(Config{})

	svc.onBusEvent(missedRunEvent("task-x"))

	select {
	case ev := <-svc.ingressCh:
		assert.Equal(t, KindRunMissed, ev.Kind)
	default:
		t.Fatal("with no mute configured, run.missed must route by default")
	}
}

// TestServiceStart_IsIdempotent covers the early-return branch when Start is
// called twice in a row.
func TestServiceStart_IsIdempotent(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	require.NoError(t, svc.Start(context.Background()))
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = svc.Stop(stopCtx)
	})
	require.NoError(t, svc.Start(context.Background()),
		"second Start must be a no-op, not an error")
}

// TestServiceStart_WithoutBusWarnsAndRunsStopped covers the "no bus" branch
// where Start returns without subscribing.
func TestServiceStart_WithoutBusWarnsAndRunsStopped(t *testing.T) {
	svc := New(Config{Bus: nil})
	require.NoError(t, svc.Start(context.Background()))
	// Stop after a bus-less Start is also a no-op via the started=false guard.
	require.NoError(t, svc.Stop(context.Background()))
}

// TestServiceStop_NotStartedReturnsNil covers the "not started" early-return.
func TestServiceStop_NotStartedReturnsNil(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	require.NoError(t, svc.Stop(context.Background()))
}

// TestServiceStop_DoubleStopReturnsNil covers the second-call CompareAndSwap
// branch in Stop.
func TestServiceStop_DoubleStopReturnsNil(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	require.NoError(t, svc.Start(context.Background()))
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, svc.Stop(stopCtx))
	require.NoError(t, svc.Stop(context.Background()),
		"repeated Stop calls must short-circuit, not error")
}

// TestServiceStop_FastWhenIdle exercises the shutdown path. With the retention
// loop wired to the same context as the dispatcher it would block until the
// caller-supplied deadline expired every time, because runRetention had no
// happy-path exit signal — its goroutine kept the WaitGroup pinned, and Stop
// only escaped via the timeout branch.
func TestServiceStop_FastWhenIdle(t *testing.T) {
	svc := New(Config{
		Bus:            events.NewEventBus(),
		Channels:       nil,
		Rules:          nil,
		FailureSink:    nil,
		RetentionEvery: time.Hour,
		RetentionFn:    func(context.Context) {},
	})

	require.NoError(t, svc.Start(context.Background()))

	deadline := 2 * time.Second
	stopCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	require.NoError(t, svc.Stop(stopCtx))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, deadline/2,
		"Stop must return promptly when there's no work to drain; took %s of %s",
		elapsed, deadline)
}

// TestOnBusEvent_SendsToIngress covers the happy-path branch of onBusEvent —
// a recognized event arrives, MapEvent returns a non-nil Event, and the event
// fits into the ingress channel.
func TestOnBusEvent_SendsToIngress(t *testing.T) {
	svc := New(Config{})

	svc.onBusEvent(failedRunEvent())

	select {
	case ev := <-svc.ingressCh:
		assert.NotNil(t, ev)
		assert.Equal(t, KindRunFailed, ev.Kind)
	default:
		t.Fatal("expected an event in ingressCh")
	}
}

// TestOnBusEvent_DropsWhenFull covers the backpressure branch: when ingressCh
// is full, the non-blocking default branch fires and droppedIngress increments.
func TestOnBusEvent_DropsWhenFull(t *testing.T) {
	svc := New(Config{})
	for i := 0; i < cap(svc.ingressCh); i++ {
		svc.ingressCh <- &Event{}
	}

	before := svc.droppedIngress.Load()
	svc.onBusEvent(failedRunEvent())
	assert.Equal(t, before+1, svc.droppedIngress.Load(),
		"event must be dropped and the counter incremented when ingress is full")
}

// TestOnBusEvent_IgnoresUnknownEvents covers the early-return branch when
// MapEvent returns nil (e.g. EventLogLine or RunEvent without a Run).
func TestOnBusEvent_IgnoresUnknownEvents(t *testing.T) {
	svc := New(Config{})
	before := svc.droppedIngress.Load()
	svc.onBusEvent(events.Event{
		Type:      events.EventLogLine,
		Timestamp: time.Now(),
	})
	assert.Equal(t, before, svc.droppedIngress.Load(),
		"ignored events must not bump the dropped counter")
	assert.Empty(t, svc.ingressCh)
}

// TestOnBusEvent_AfterStopDropsWithoutPanic pins the M1 fix: Stop closes
// ingressCh, but an onBusEvent that captured the subscriber list just before
// unsubscribe may still be mid-send. The old code did a bare close, so that
// late send panicked ("send on closed channel"). The fix guards the close and
// the send with ingressMu + an ingressClosed flag: a post-Stop onBusEvent must
// observe the closed state and drop, never panic. We drive the send path
// directly after Stop — with an empty (drained) channel the send branch, not
// the backpressure default, is what would have panicked pre-fix.
func TestOnBusEvent_AfterStopDropsWithoutPanic(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	require.NoError(t, svc.Start(context.Background()))

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, svc.Stop(stopCtx))

	before := svc.droppedIngress.Load()

	// Simulate the in-flight publish that raced Stop: onBusEvent runs after the
	// channel was closed. Wrap in a goroutine with recover so a regression
	// (bare send on the closed channel) fails the test instead of crashing it.
	panicked := make(chan any, 1)
	go func() {
		defer func() { panicked <- recover() }()
		svc.onBusEvent(failedRunEvent())
	}()

	select {
	case r := <-panicked:
		require.Nil(t, r, "onBusEvent after Stop must not panic on the closed ingress channel")
	case <-time.After(2 * time.Second):
		t.Fatal("onBusEvent did not return after Stop")
	}

	assert.Equal(t, before+1, svc.droppedIngress.Load(),
		"an event arriving after Stop must be counted as a drop, not sent on the closed channel")
}

// TestServiceStop_RetentionTickerExits ensures the retention goroutine actually
// returns rather than leaking. We invoke Stop with a generous deadline and
// verify it doesn't hit the timeout branch (where ctx is cancelled).
func TestServiceStop_RetentionTickerExits(t *testing.T) {
	var ticks atomic.Int32
	svc := New(Config{
		Bus:            events.NewEventBus(),
		RetentionEvery: 10 * time.Millisecond,
		RetentionFn:    func(context.Context) { ticks.Add(1) },
	})

	require.NoError(t, svc.Start(context.Background()))

	require.Eventually(t, func() bool { return ticks.Load() > 0 }, time.Second, 5*time.Millisecond,
		"retention loop should fire while the service is running")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, svc.Stop(stopCtx))

	assert.NoError(t, stopCtx.Err(), "Stop must not consume its own deadline when idle")

	prior := ticks.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, prior, ticks.Load(), "retention loop must stop ticking after Stop returns")
}
