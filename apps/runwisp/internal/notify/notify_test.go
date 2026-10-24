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

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
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

// TestOnBusEvent_MissedReachesIngress confirms a run.missed event is mapped and
// enqueued for routing; onBusEvent does not filter by task. Whether it actually
// pages is decided downstream by the classified failure bit (run.IsFailure) against the failure route, covered by
// the MatchFailure predicate and configload route-compilation tests.
func TestOnBusEvent_MissedReachesIngress(t *testing.T) {
	svc := New(Config{})

	svc.onBusEvent(missedRunEvent("task-x"))

	select {
	case ev := <-svc.ingressCh:
		assert.Equal(t, KindRunMissed, ev.Kind)
	default:
		t.Fatal("run.missed must be mapped and enqueued for routing")
	}
}

// TestServiceStart_IsIdempotent covers the early-return branch when Start is
// called twice in a row.
func TestServiceStart_IsIdempotent(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	svc.Start(context.Background())
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		svc.Stop(stopCtx)
	})
	svc.Start(context.Background()) // must be a no-op, not a second set of workers
}

// TestServiceStop_NotStartedIsNoOp covers the "not started" early-return.
func TestServiceStop_NotStartedIsNoOp(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	svc.Stop(context.Background())
}

// TestServiceStop_DoubleStopIsNoOp covers the second-call CompareAndSwap
// branch in Stop.
func TestServiceStop_DoubleStopIsNoOp(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	svc.Start(context.Background())
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(stopCtx)
	svc.Stop(context.Background())
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

	svc.Start(context.Background())

	deadline := 2 * time.Second
	stopCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	svc.Stop(stopCtx)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, deadline/2,
		"Stop must return promptly when there's no work to drain; took %s of %s",
		elapsed, deadline)
}

// TestServiceStop_KeepsRetriesWithoutCut pins that Stop alone (how a reload
// retires the old service) still retries a delivery that failed once, so a
// reload during a brief outage doesn't lose the alert.
func TestServiceStop_KeepsRetriesWithoutCut(t *testing.T) {
	var failures atomic.Int64
	flaky := &executeChannel{id: "flaky"}
	flaky.execFn = func(ctx context.Context, _ *Event) error {
		return RetryWithBackoff(ctx, BackoffConfig{InitialInterval: 20 * time.Millisecond}, func(context.Context) error {
			if failures.Add(1) == 1 {
				return errors.New("503")
			}
			return nil
		})
	}
	svc := New(Config{
		Bus:      events.NewEventBus(),
		Channels: []Channel{flaky},
		Rules:    []Rule{{Match: MatchAll(), ActionIDs: []string{"flaky"}}},
	})
	svc.Start(context.Background())
	svc.onBusEvent(failedRunEvent())

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.Stop(stopCtx)

	assert.Equal(t, int64(2), failures.Load(), "the failed attempt must be retried")
}

// TestServiceCutRetries_SkipsRetryBackoff pins that shutdown doesn't wait out a
// failing delivery's backoff: the attempt in flight finishes, but no retry is
// scheduled. A notifier pointed at an unreachable endpoint (the demo's
// .invalid Slack webhook) used to hold the daemon for the whole drain deadline.
func TestServiceCutRetries_SkipsRetryBackoff(t *testing.T) {
	attempted := make(chan struct{}, 1)
	dead := &executeChannel{id: "dead"}
	dead.execFn = func(ctx context.Context, _ *Event) error {
		return RetryWithBackoff(ctx, BackoffConfig{InitialInterval: time.Minute}, func(context.Context) error {
			select {
			case attempted <- struct{}{}:
			default:
			}
			return errors.New("unreachable")
		})
	}
	svc := New(Config{
		Bus:      events.NewEventBus(),
		Channels: []Channel{dead},
		Rules:    []Rule{{Match: MatchAll(), ActionIDs: []string{"dead"}}},
	})
	svc.Start(context.Background())
	svc.onBusEvent(failedRunEvent())

	select {
	case <-attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("delivery was never attempted")
	}

	deadline := 5 * time.Second
	stopCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	start := time.Now()
	svc.CutRetries()
	svc.Stop(stopCtx)

	assert.Less(t, time.Since(start), time.Second, "Stop must not sit in the retry backoff until its deadline")
	assert.Equal(t, int64(1), dead.hits.Load())
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

// TestOnBusEvent_AfterStopDropsWithoutPanic: Stop closes ingressCh, but an
// onBusEvent that captured the subscriber list just before unsubscribe may
// still be mid-send. ingressMu + the ingressClosed flag make a post-Stop
// onBusEvent observe the closed state and drop, never panic with "send on
// closed channel". We drive the send path directly after Stop: with an empty
// (drained) channel the send branch, not the backpressure default, is the one
// at risk.
func TestOnBusEvent_AfterStopDropsWithoutPanic(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	svc.Start(context.Background())

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(stopCtx)

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

	svc.Start(context.Background())

	require.Eventually(t, func() bool { return ticks.Load() > 0 }, time.Second, 5*time.Millisecond,
		"retention loop should fire while the service is running")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	svc.Stop(stopCtx)

	assert.NoError(t, stopCtx.Err(), "Stop must not consume its own deadline when idle")

	prior := ticks.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, prior, ticks.Load(), "retention loop must stop ticking after Stop returns")
}

// runRecoveredService runs a service that routes failures and recoveries (as
// the built-in alert routes do) to one channel, publishes a success of task-x,
// stops the service so every delivery has landed, and returns them.
func runRecoveredService(t *testing.T, prevFailed bool) []*Event {
	t.Helper()
	var (
		mu   sync.Mutex
		got  []*Event
		asks []string
	)
	ch := &executeChannel{id: "a", execFn: func(_ context.Context, ev *Event) error {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, ev)
		return nil
	}}
	bus := events.NewEventBus()
	svc := New(Config{
		Bus:      bus,
		Channels: []Channel{ch},
		Rules: []Rule{
			{Match: MatchFailure(), ActionIDs: []string{"a"}},
			{Match: MatchOutcomes("recovered"), ActionIDs: []string{"a"}},
		},
		PrevRunFailed: func(_ context.Context, task, runID string) (bool, error) {
			asks = append(asks, task+"/"+runID)
			return prevFailed, nil
		},
	})
	svc.Start(context.Background())
	success := model.ReasonSuccess
	bus.Publish(events.EventRunCompleted, events.RunEvent{Run: &model.Run{
		ID: "01HOK", TaskName: "task-x", Status: model.PhaseEnded, EndReason: &success,
	}})
	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	svc.Stop(stopCtx)
	assert.Equal(t, []string{"task-x/01HOK"}, asks, "the lookup skips the run that just succeeded")
	return got
}

func TestService_RecoveredAfterFailedRun(t *testing.T) {
	got := runRecoveredService(t, true)
	require.Len(t, got, 1, "only the recovery is routed, not the success itself")
	assert.Equal(t, KindRunRecovered, got[0].Kind)
	assert.Equal(t, SevInfo, got[0].Severity)
	assert.False(t, got[0].IsFailure)
	assert.Equal(t, "recovered", got[0].Outcome())
	assert.Equal(t, "01HOK", got[0].Run.ID)
}

func TestService_NoRecoveredWhenPreviousSucceeded(t *testing.T) {
	assert.Empty(t, runRecoveredService(t, false))
}
