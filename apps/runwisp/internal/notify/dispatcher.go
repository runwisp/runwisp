// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	router   *Router
	channels map[string]Channel
	clock    func() time.Time
	failures SyntheticIngester
	logger   *slog.Logger

	queues  map[string]chan *Event
	workers sync.WaitGroup

	droppedAction atomic.Uint64
}

func newDispatcher(router *Router, channels map[string]Channel, queueSize int, clock func() time.Time, failures SyntheticIngester, logger *slog.Logger) *dispatcher {
	if queueSize <= 0 {
		queueSize = DefaultActionQueueSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	queues := make(map[string]chan *Event, len(channels))
	for id := range channels {
		queues[id] = make(chan *Event, queueSize)
	}
	return &dispatcher{
		router:   router,
		channels: channels,
		clock:    clock,
		failures: failures,
		logger:   logger,
		queues:   queues,
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
	for _, c := range d.router.Route(ev) {
		queue, ok := d.queues[c.ID()]
		if !ok {
			continue
		}
		SendDropOldest(queue, ev, func() {
			d.droppedAction.Add(1)
			d.logger.Warn("notify dispatcher queue full; dropping oldest",
				"action", c.ID(), "kind", string(ev.Kind))
		})
	}
}

// SendDropOldest delivers v on ch, evicting the oldest buffered value when ch
// is full so the freshest signal always reaches the receiver. onEvict (may be
// nil) runs once per eviction. Every step is non-blocking; the loop retries
// only when a racing receiver drained ch between the send and the eviction.
func SendDropOldest[T any](ch chan T, v T, onEvict func()) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
			if onEvict != nil {
				onEvict()
			}
		default:
		}
	}
}

func (d *dispatcher) closeQueues() {
	for _, q := range d.queues {
		close(q)
	}
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
	if cErr := ctx.Err(); cErr != nil && errors.Is(err, cErr) {
		// The worker's own ctx was cancelled (daemon shutdown), so this is not
		// a real delivery failure. Matching the sentinel alone is not enough:
		// an HTTP channel's own per-request timeout also surfaces as
		// context.DeadlineExceeded while this ctx is still live, and that is a
		// reportable failure.
		return
	}
	if d.failures == nil {
		d.logger.Error("notify delivery exhausted retries", "action", id, "cause", err.Error())
		return
	}
	ReportDeliveryFailure(d.failures, d.clock, id, ev, err)
	d.logger.Error("notify delivery exhausted retries; surfacing in-app",
		"action", id, "cause", err.Error())
}

// DroppedActionCount returns the cumulative number of events dropped because
// a per-action queue was full. Cleared only by service restart.
func (d *dispatcher) DroppedActionCount() uint64 {
	return d.droppedAction.Load()
}

// ReportDeliveryFailure emits the synthetic in-app notify_delivery_failed event
// for a permanently failed outbound delivery. Exported so the outbound-coalesce
// wrapper (internal/notify/coalesce), whose window-close summary delivers
// asynchronously outside the dispatcher's synchronous Execute path, can surface
// its own permanent failures through the identical event shape and in-app sink.
func ReportDeliveryFailure(sink SyntheticIngester, clock func() time.Time, actionID string, original *Event, cause error) {
	syn := &Event{
		ID:        ulid.Make().String(),
		Kind:      KindNotifyDeliveryFailed,
		Severity:  SevWarn,
		Timestamp: clock(),
		Reason:    cause.Error(),
		Extra: map[string]any{
			"channel": actionID,
		},
	}
	if original != nil {
		syn.TaskName = original.TaskName
		syn.Extra["original_kind"] = string(original.Kind)
		syn.Extra["task_name"] = original.TaskName
	}
	sink.IngestSynthetic(syn)
}
