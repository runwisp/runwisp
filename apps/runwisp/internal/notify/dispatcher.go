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
)

// Clocker is the time source used by the dispatcher and coalescer. The
// production wire is time.Now; unit tests use testutil.FakeClock.
type Clocker interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// RealClock returns the production clock implementation. Convenience to avoid
// callers stating their own.
func RealClock() Clocker { return realClock{} }

// SyntheticIngester receives synthetic delivery-failure events. The in-app channel's
// Coalescer fits this shape; bypassing the router is what prevents cycles
// (a Slack failure must never re-route as a Slack notification).
type SyntheticIngester interface {
	IngestSynthetic(ev *Event)
}

// dispatcher pumps events from the ingress channel through the router to
// per-channel queues, where workers execute side effects with retry and
// surface permanent failures back via the SyntheticIngester.
type dispatcher struct {
	router    *Router
	channels  map[string]Channel
	queueSize int
	clock     Clocker
	failures  SyntheticIngester
	logger    *slog.Logger

	queues  map[string]chan *Event
	workers sync.WaitGroup

	droppedAction atomic.Uint64
}

func newDispatcher(router *Router, channels map[string]Channel, queueSize int, clock Clocker, failures SyntheticIngester, logger *slog.Logger) *dispatcher {
	if queueSize <= 0 {
		queueSize = 256
	}
	if logger == nil {
		logger = slog.Default()
	}
	queues := make(map[string]chan *Event, len(channels))
	for id := range channels {
		queues[id] = make(chan *Event, queueSize)
	}
	return &dispatcher{
		router:    router,
		channels:  channels,
		queueSize: queueSize,
		clock:     clock,
		failures:  failures,
		logger:    logger,
		queues:    queues,
	}
}

// startWorkers spawns one worker goroutine per channel. Workers exit when
// their queue is closed AND drained, or when ctx is cancelled.
func (d *dispatcher) startWorkers(ctx context.Context) {
	for id, c := range d.channels {
		queue := d.queues[id]
		d.workers.Add(1)
		go d.worker(ctx, id, c, queue)
	}
}

// dispatch enqueues an event into every matching channel's queue.
// Drop-oldest under pressure: when the queue is full we drain one slot
// (the oldest waiting event) before retrying the send.
func (d *dispatcher) dispatch(ev *Event) {
	if ev == nil {
		return
	}
	for _, c := range d.router.Route(ev) {
		queue, ok := d.queues[c.ID()]
		if !ok {
			continue
		}
		d.enqueueDropOldest(c.ID(), queue, ev)
	}
}

// enqueueDropOldest sends ev to queue, evicting the oldest queued event under
// sustained pressure so the freshest signal always reaches the worker.
func (d *dispatcher) enqueueDropOldest(actionID string, queue chan *Event, ev *Event) {
	for {
		select {
		case queue <- ev:
			return
		default:
		}
		select {
		case <-queue:
			d.droppedAction.Add(1)
			d.logger.Warn("notify dispatcher queue full; dropping oldest",
				"action", actionID, "kind", string(ev.Kind))
		default:
			// Worker drained between the two selects; loop back and try the send.
		}
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

func (d *dispatcher) worker(ctx context.Context, id string, ch Channel, queue <-chan *Event) {
	defer d.workers.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-queue:
			if !ok {
				return
			}
			d.executeOne(ctx, id, ch, ev)
		}
	}
}

func (d *dispatcher) executeOne(ctx context.Context, id string, ch Channel, ev *Event) {
	defer func() {
		if r := recover(); r != nil {
			d.logger.Error("notify channel panicked", "channel", id, "panic", r)
		}
	}()
	err := ch.Execute(ctx, ev)
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if d.failures == nil {
		d.logger.Error("notify delivery exhausted retries", "action", id, "cause", err.Error())
		return
	}
	reportDeliveryFailure(d.failures, d.clock, id, ev, err)
	d.logger.Error("notify delivery exhausted retries; surfacing in-app",
		"action", id, "cause", err.Error())
}

// DroppedActionCount returns the cumulative number of events dropped because
// a per-action queue was full. Cleared only by service restart.
func (d *dispatcher) DroppedActionCount() uint64 {
	return d.droppedAction.Load()
}
