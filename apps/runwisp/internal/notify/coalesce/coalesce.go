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

// Channel wraps a delegate notify.Channel with outbound coalescing. It is
// itself a notify.Channel — the dispatcher pumps events through it like any
// other.
type Channel struct {
	inner  notify.Channel
	cfg    Config
	clock  notify.Clock
	logger *slog.Logger

	mu    sync.Mutex
	state map[string]*fpState

	// timerCtx underpins window-close summaries. Cancelling it stops pending
	// summary deliveries; Close blocks until in-flight summaries return.
	timerCtx    context.Context
	timerCancel context.CancelFunc
	wg          sync.WaitGroup
}

type fpState struct {
	lastSent  time.Time
	pending   int
	lastEvent *notify.Event
	timer     *time.Timer
}

// New wraps inner. The returned Channel must be Closed; otherwise pending
// timer goroutines will leak.
func New(inner notify.Channel, cfg Config, clock notify.Clock, logger *slog.Logger) *Channel {
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
		state:       make(map[string]*fpState),
		timerCtx:    ctx,
		timerCancel: cancel,
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

	fp := fingerprintKey(ev)
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
		st.timer = time.AfterFunc(deadline, func() {
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
	count := st.pending // suppressed events folded into the window-close summary
	ev := st.lastEvent
	st.pending = 0
	st.lastSent = c.clock.Now()
	st.lastEvent = nil
	st.timer = nil
	c.mu.Unlock()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		if err := c.inner.Execute(c.timerCtx, summarize(ev, count, true)); err != nil {
			c.logger.Error("notify outbound coalesce: window-close summary delivery failed",
				"channel", c.inner.ID(), "fingerprint", fp, "err", err)
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

// fingerprintKey mirrors the in-app coalescer's fingerprint shape: kind +
// task name + (end_reason | delivery target). Two events that should be
// folded together share this key.
func fingerprintKey(ev *notify.Event) string {
	if ev == nil {
		return ""
	}
	extra := ""
	if ev.Run != nil && ev.Run.EndReason != nil {
		extra = string(*ev.Run.EndReason)
	}
	if ev.Kind == notify.KindNotifyDeliveryFailed && ev.Extra != nil {
		ch, _ := ev.Extra["channel"].(string)
		ok, _ := ev.Extra["original_kind"].(string)
		extra = ch + "|" + ok
	}
	return ev.Kind.FingerprintToken() + "|" + ev.TaskName + "|" + extra
}
