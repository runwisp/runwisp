// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"sync"
	"time"
)

// Clock is a controllable time source for code that takes the runtime's
// `func() time.Time` clock contract (e.g. NewTaskManager, NewScheduler). It
// lets tests stamp deterministic timestamps instead of passing time.Now, so
// CreatedAt/StartAt/EndTime assertions are exact rather than fuzzy.
//
// Pass the bound method value as the clock: NewTaskManager(exec, eb, clk.Now).
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

// NewClock returns a Clock fixed at t. The zero time is a poor choice for run
// timestamps, so callers should pass a realistic instant.
func NewClock(t time.Time) *Clock { return &Clock{now: t} }

// Now reports the current fake time. Its signature matches `func() time.Time`,
// so `clk.Now` can be handed directly to constructors expecting that clock.
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

// Set pins the clock to an absolute instant.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
