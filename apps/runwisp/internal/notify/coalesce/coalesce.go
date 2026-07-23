// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package coalesce wraps an outbound notify.Channel (Slack, Telegram, …)
// with fingerprint-based coalescing so a flapping task doesn't translate
// to one outbound delivery per failure.
//
// Strategy, matching the pre-1.0 design call in /concepts/concurrency:
//
//   - The first event seen in a window for a given fingerprint is forwarded
//     immediately.
//   - Subsequent events within the window are suppressed until either
//     `everyN` accumulate (the Nth event is forwarded with a coalesced_count
//     marker), or the window expires while a pending event is still buffered
//     (a "summary" event is forwarded carrying the count and the latest
//     payload).
//
// In-app notifications go through inapp.Coalescer (which folds into a single
// SQLite row); this wrapper is for outbound channels where each delivery is
// a real HTTP call to a paging surface.
package coalesce

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
)

// Config tunes the coalescing window.
type Config struct {
	// Window matches repeats whose lastSent is within this span.
	Window time.Duration
	// EveryN is the count at which the wrapper forwards a coalesced event
	// without waiting for the window to close. 0/1 disables this short-cut
	// and only the window-close summary fires.
	EveryN int
}

// Default values for Config.
const (
	DefaultWindow = time.Hour
	DefaultEveryN = 10
)

// timerStopper is the slice of *time.Timer that coalesce relies on: the ability
// to cancel a scheduled window-close flush. Tests inject a fake to fire
// flushes deterministically instead of waiting on the wall clock.
type timerStopper interface {
	Stop() bool
}

// afterFunc schedules fn after d and returns a handle to cancel it. Production
// wires time.AfterFunc; tests substitute a controllable fake.
type afterFunc func(d time.Duration, fn func()) timerStopper

// Channel wraps a delegate notify.Channel with outbound coalescing. It is
// itself a notify.Channel — the dispatcher pumps events through it like any
// other.
type Channel struct {
	inner  notify.Channel
	cfg    Config
	clock  notify.Clocker
	logger *slog.Logger
	after  afterFunc
	// failures surfaces a permanently-failed window-close summary as an in-app
	// notify_delivery_failed event. Immediate (actionForward/Summary) deliveries
	// already reach the dispatcher's failure path via the Execute return value;
	// this covers the async timerFlush path, which the dispatcher never sees. Nil
	// (e.g. no in-app channel) falls back to logging only.
	failures notify.SyntheticIngester

	mu    sync.Mutex
	state map[string]*fpState

	// timerDone is closed when the channel is closed; timer goroutines select on
	// it before dispatching window-close summaries. Close blocks until all
	// in-flight summary goroutines return.
	timerDone   <-chan struct{}
	timerCancel context.CancelFunc
	wg          sync.WaitGroup
}

type fpState struct {
	lastSent  time.Time
	pending   int
	lastEvent *notify.Event
	timer     timerStopper
}

// New wraps inner. The returned Channel must be Closed; otherwise pending
// timer goroutines will leak. failures (typically the in-app channel) receives
// a synthetic delivery-failure event when a window-close summary permanently
// fails; pass nil to log only.
func New(inner notify.Channel, cfg Config, clock notify.Clocker, logger *slog.Logger, failures notify.SyntheticIngester) *Channel {
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.EveryN < 0 {
		cfg.EveryN = 0
	}
	if clock == nil {
		clock = notify.RealClock()
	}
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Channel{
		inner:       inner,
		cfg:         cfg,
		clock:       clock,
		logger:      logger,
		after:       func(d time.Duration, fn func()) timerStopper { return time.AfterFunc(d, fn) },
		state:       make(map[string]*fpState),
		timerDone:   ctx.Done(),
		timerCancel: cancel,
		failures:    failures,
	}
}

// ID delegates to the wrapped channel so the dispatcher's per-channel queue
// keys remain stable.
func (c *Channel) ID() string { return c.inner.ID() }

// Execute decides whether to forward, forward-as-summary, or suppress the
// event. Suppressed events are released later by the window-close timer.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	if ev == nil {
		return nil
	}
	d := c.decide(ev)
	switch d.action {
	case actionForward:
		return c.inner.Execute(ctx, ev)
	case actionForwardSummary:
		return c.inner.Execute(ctx, summarize(ev, d.count, false))
	case actionSuppress:
		return nil
	default:
		return nil
	}
}

// Close stops the timer goroutines and closes the wrapped channel.
func (c *Channel) Close(ctx context.Context) error {
	c.timerCancel()

	c.mu.Lock()
	for _, st := range c.state {
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
	}
	c.mu.Unlock()

	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return c.inner.Close(ctx)
}

type decisionAction int

const (
	actionForward        decisionAction = iota // first in window — pass through
	actionForwardSummary                       // Nth or window-close — forward with summary metadata
	actionSuppress                             // suppressed; window-close timer will deliver later
)

type decision struct {
	action decisionAction
	count  int
}

func (c *Channel) decide(ev *notify.Event) decision {
	c.mu.Lock()
	defer c.mu.Unlock()

	fp := notify.FingerprintKey(ev)
	now := c.clock.Now()
	st, ok := c.state[fp]

	if !ok {
		c.state[fp] = &fpState{lastSent: now}
		return decision{action: actionForward}
	}

	if now.Sub(st.lastSent) >= c.cfg.Window {
		// Window already closed without a summary firing (shouldn't happen
		// when timer fired, but guard against drift). Treat as a fresh window.
		st.lastSent = now
		st.pending = 0
		st.lastEvent = nil
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		return decision{action: actionForward}
	}

	st.pending++
	st.lastEvent = ev

	if c.cfg.EveryN > 1 && st.pending >= c.cfg.EveryN {
		// pending was incremented above so it counts the current event as
		// well as all suppressed-since-last-delivery events. The summary's
		// coalesced_count is exactly that — events folded into this single
		// outbound delivery.
		count := st.pending
		st.pending = 0
		st.lastSent = now
		st.lastEvent = nil
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		return decision{action: actionForwardSummary, count: count}
	}

	if st.timer == nil {
		deadline := st.lastSent.Add(c.cfg.Window).Sub(now)
		if deadline < 0 {
			deadline = 0
		}
		st.timer = c.after(deadline, func() {
			c.timerFlush(fp)
		})
	}
	return decision{action: actionSuppress}
}

func (c *Channel) timerFlush(fp string) {
	c.mu.Lock()
	st, ok := c.state[fp]
	if !ok || st.pending == 0 || st.lastEvent == nil {
		if ok {
			st.timer = nil
		}
		c.mu.Unlock()
		return
	}
	// Skip the flush if Close has begun. Close closes timerDone (via
	// timerCancel) before it acquires c.mu, so checking it here — and doing the
	// wg.Add below — under c.mu makes the check-and-register atomic against
	// Close. Without this, a timer that fired just as Close started could call
	// wg.Add concurrently with Close's wg.Wait and panic ("WaitGroup reused").
	select {
	case <-c.timerDone:
		st.timer = nil
		c.mu.Unlock()
		return
	default:
	}

	count := st.pending // suppressed events folded into the window-close summary
	ev := st.lastEvent
	st.pending = 0
	st.lastSent = c.clock.Now()
	st.lastEvent = nil
	st.timer = nil
	// Registered under c.mu, before Close can take the lock and start wg.Wait.
	c.wg.Add(1)
	c.mu.Unlock()

	go func() {
		defer c.wg.Done()
		select {
		case <-c.timerDone:
			return
		default:
		}
		if err := c.inner.Execute(context.Background(), summarize(ev, count, true)); err != nil {
			c.logger.Error("notify outbound coalesce: window-close summary delivery failed",
				"channel", c.inner.ID(), "fingerprint", fp, "err", err)
			// Async path the dispatcher never observes: surface the permanent
			// failure in-app ourselves, matching the uncoalesced path, so a
			// coalesced burst that fails at window close is not silent.
			if c.failures != nil {
				notify.ReportDeliveryFailure(c.failures, c.clock, c.inner.ID(), ev, err)
			}
		}
	}()
}

// summarize annotates ev with coalesced metadata so renderers can append a
// "(12× in the last hour)" suffix. The mutation is on a shallow copy — the
// caller's Event is not modified.
func summarize(ev *notify.Event, count int, windowClose bool) *notify.Event {
	out := *ev
	if out.Extra == nil {
		out.Extra = make(map[string]any, 2)
	} else {
		// shallow-copy the map so we don't mutate the caller's
		dup := make(map[string]any, len(out.Extra)+2)
		for k, v := range out.Extra {
			dup[k] = v
		}
		out.Extra = dup
	}
	out.Extra["coalesced_count"] = count
	if windowClose {
		out.Extra["coalesced_summary"] = true
	}
	return &out
}
