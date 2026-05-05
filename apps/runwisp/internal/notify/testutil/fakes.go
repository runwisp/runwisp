// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package testutil holds fakes used across notify subpackages. They live in
// their own package so unit tests can wire them up without exporting
// implementation details from production code.
package testutil

import (
	"context"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
)

// FakeChannel records every Execute call. Optionally returns a configured
// error to simulate transient or permanent failures.
type FakeChannel struct {
	IDValue string
	MatchFn func(*notify.Event) bool
	Err     error

	mu       sync.Mutex
	received []*notify.Event
	closed   bool
}

func NewFakeChannel(id string) *FakeChannel {
	return &FakeChannel{IDValue: id}
}

func (f *FakeChannel) ID() string { return f.IDValue }
func (f *FakeChannel) Match(ev *notify.Event) bool {
	if f.MatchFn != nil {
		return f.MatchFn(ev)
	}
	return true
}

func (f *FakeChannel) Execute(ctx context.Context, ev *notify.Event) error {
	f.mu.Lock()
	f.received = append(f.received, ev)
	err := f.Err
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (f *FakeChannel) Close(context.Context) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// Received returns a copy of every event Execute observed, in arrival order.
func (f *FakeChannel) Received() []*notify.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*notify.Event, len(f.received))
	copy(out, f.received)
	return out
}

// Closed reports whether Close has been called.
func (f *FakeChannel) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// FakeClock returns a controlled time. Set Now to a fixed value; Advance
// shifts it forward.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{now: t} }

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// FakeHub captures Hub.Publish calls for verification.
type FakeHub struct {
	mu       sync.Mutex
	captured []HubEvent
}

type HubEvent struct {
	Type string
	Data any
}

func NewFakeHub() *FakeHub { return &FakeHub{} }

func (h *FakeHub) Publish(eventType string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.captured = append(h.captured, HubEvent{Type: eventType, Data: data})
}

func (h *FakeHub) Captured() []HubEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]HubEvent, len(h.captured))
	copy(out, h.captured)
	return out
}
