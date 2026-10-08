// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package fakeclock holds the hand-driven time fakes. It depends only on the
// standard library and afterfunc, so any package's tests (including ones that
// testutil itself imports) can use it.
package fakeclock

import (
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/afterfunc"
)

// Clock is a controllable time source for code that takes a `func() time.Time`
// clock. Pass the bound method value: NewTaskManager(exec, eb, clk.Now).
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// New returns a Clock fixed at t.
func New(t time.Time) *Clock { return &Clock{now: t} }

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Timers is a deterministic afterfunc.Func: After records each callback and the
// test fires them on demand with FireAll instead of waiting on the wall clock.
type Timers struct {
	mu     sync.Mutex
	timers []*timer
}

// timer is one armed callback. Stop and FireAll flip stopped under the parent's
// lock, so a Stop called from inside a callback never races the firing loop.
type timer struct {
	owner   *Timers
	delay   time.Duration
	fn      func()
	stopped bool
}

func (t *timer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	was := t.stopped
	t.stopped = true
	return !was
}

// After records fn and returns its handle; it satisfies afterfunc.Func.
func (m *Timers) After(d time.Duration, fn func()) afterfunc.Stopper {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &timer{owner: m, delay: d, fn: fn}
	m.timers = append(m.timers, t)
	return t
}

// Pending counts armed timers that have neither fired nor been stopped.
func (m *Timers) Pending() int { return len(m.Armed()) }

// Armed returns the delays of the still-armed timers, in arming order.
func (m *Timers) Armed() []time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []time.Duration
	for _, t := range m.timers {
		if !t.stopped {
			out = append(out, t.delay)
		}
	}
	return out
}

// FireAll invokes every still-armed callback once, in arming order. Callbacks
// run without the lock held so they can re-enter and Stop other timers.
func (m *Timers) FireAll() {
	m.mu.Lock()
	var due []*timer
	for _, t := range m.timers {
		if !t.stopped {
			t.stopped = true
			due = append(due, t)
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
}
