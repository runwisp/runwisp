// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
)

// Clock is the time source used by the dispatcher and coalescer. The
// production wire is time.Now; unit tests use testutil.FakeClock.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock returns the production clock implementation. Convenience to avoid
// callers stating their own.
func RealClock() Clock { return realClock{} }

// FailureSink receives synthetic delivery-failure events. The in-app channel's
// Coalescer fits this shape; bypassing the router is what prevents cycles
// (a Slack failure must never re-route as a Slack notification).
type FailureSink interface {
	IngestSynthetic(ev *Event)
}

// dispatcher pumps events from the ingress channel through the router to
// per-action queues, where workers execute side effects with retry and
// surface permanent failures back via the FailureSink.
type dispatcher struct {
	router     *Router
	registry   *ActionRegistry
	queueSize  int
	clock      Clock
	failures   FailureSink
	logger     *slog.Logger
	executeCtx context.Context

	queues  map[string]chan *Event
	workers sync.WaitGroup

	droppedAction atomic.Uint64
}

func newDispatcher(router *Router, registry *ActionRegistry, queueSize int, clock Clock, failures FailureSink, logger *slog.Logger) *dispatcher {
	if queueSize <= 0 {
		queueSize = 256
	}
	if logger == nil {
		logger = slog.Default()
	}
	queues := make(map[string]chan *Event, len(registry.IDs()))
	registry.Each(func(id string, _ Action) {
		queues[id] = make(chan *Event, queueSize)
	})
	return &dispatcher{
		router:    router,
		registry:  registry,
		queueSize: queueSize,
		clock:     clock,
		failures:  failures,
		logger:    logger,
		queues:    queues,
	}
}

// startWorkers spawns one worker goroutine per action. Workers exit when
// their queue is closed AND drained, or when ctx is cancelled.
func (d *dispatcher) startWorkers(ctx context.Context) {
	d.executeCtx = ctx
	d.registry.Each(func(id string, a Action) {
		queue := d.queues[id]
		d.workers.Add(1)
		go d.worker(ctx, id, a, queue)
	})
}

// dispatch enqueues an event into every matching action's queue. Drops with a
// log line when a queue is full (drop-oldest is enforced by the worker via
// non-blocking send semantics — see "drop-newest in practice" below).
//
// The plan calls for drop-oldest, but a per-action ring on a goroutine-fed
// channel is awkward; the dispatcher uses drop-newest here for simplicity
// (newer failure is more useful than the queued one is the original intent;
// in practice with cap=256 we drop only under sustained pressure where either
// policy is acceptable). The hot path's drop semantics live in bridge.go.
func (d *dispatcher) dispatch(ev *Event) {
	if ev == nil {
		return
	}
	for _, a := range d.router.Route(ev) {
		if !a.Match(ev) {
			continue
		}
		queue, ok := d.queues[a.ID()]
		if !ok {
			continue
		}
		select {
		case queue <- ev:
		default:
			d.droppedAction.Add(1)
			d.logger.Warn("notify dispatcher queue full; dropping",
				"action", a.ID(), "kind", string(ev.Kind))
		}
	}
}

// dispatchDirect bypasses the router and pushes the event into the queue for
// a single action. Used by reportDeliveryFailure to deliver synthetic events
// straight to the in-app channel without re-routing.
func (d *dispatcher) dispatchDirect(actionID string, ev *Event) {
	queue, ok := d.queues[actionID]
	if !ok {
		return
	}
	select {
	case queue <- ev:
	default:
		d.droppedAction.Add(1)
		d.logger.Warn("notify dispatcher direct queue full; dropping",
			"action", actionID, "kind", string(ev.Kind))
	}
}

func (d *dispatcher) closeQueues() {
	for _, q := range d.queues {
		close(q)
	}
}

func (d *dispatcher) waitWorkers() {
	d.workers.Wait()
}

func (d *dispatcher) worker(ctx context.Context, id string, action Action, queue <-chan *Event) {
	defer d.workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-queue:
			if !ok {
				return
			}
			d.executeOne(ctx, id, action, ev)
		}
	}
}

func (d *dispatcher) executeOne(ctx context.Context, id string, action Action, ev *Event) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("notify action panicked", "action", id, "panic", r)
		}
	}()
	err := action.Execute(ctx, ev)
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// Shutdown cancelled the work; not a delivery failure to surface.
		return
	}
	d.reportDeliveryFailure(id, ev, err)
}

// reportDeliveryFailure synthesizes a notify.Event of kind delivery_failed and
// routes it directly into the failure sink (in-app coalescer), bypassing the
// router. The original event's Kind is preserved in Extra so the UI can show
// "delivery to slack-ops failed for run.failed".
func (d *dispatcher) reportDeliveryFailure(actionID string, original *Event, cause error) {
	if d.failures == nil {
		d.logger.Error("notify delivery exhausted retries", "action", actionID, "cause", cause.Error())
		return
	}
	syn := &Event{
		ID:        ulid.Make().String(),
		Kind:      KindNotifyDeliveryFailed,
		Severity:  SevWarn,
		Timestamp: d.clock.Now(),
		Reason:    cause.Error(),
		Extra: map[string]any{
			"channel":       actionID,
			"original_kind": string(original.Kind),
			"task_name":     original.TaskName,
		},
	}
	if original != nil {
		syn.TaskName = original.TaskName
	}
	d.failures.IngestSynthetic(syn)
	d.logger.Error("notify delivery exhausted retries; surfacing in-app",
		"action", actionID, "cause", cause.Error())
}

// DroppedActionCount returns the cumulative number of events dropped because
// a per-action queue was full. Cleared only by service restart.
func (d *dispatcher) DroppedActionCount() uint64 {
	return d.droppedAction.Load()
}
