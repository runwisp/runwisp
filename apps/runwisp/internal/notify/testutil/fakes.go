// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package testutil holds fakes used across notify subpackages. They live in
// their own package so unit tests can wire them up without exporting
// implementation details from production code.
package testutil

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/stretchr/testify/require"
)

// NewFastBackoff returns a backoff tuned for tests: tiny intervals so retry
// paths exercise their logic in milliseconds instead of seconds.
func NewFastBackoff() notify.BackoffConfig {
	return notify.BackoffConfig{
		InitialInterval: 5 * time.Millisecond,
		MaxInterval:     20 * time.Millisecond,
		MaxElapsedTime:  100 * time.Millisecond,
		Multiplier:      2.0,
	}
}

// NewFastTransport returns an HTTPProvider with a short client timeout and the
// fast test backoff. Shared by every HTTP-backed channel test (slack,
// telegram, discord, webhook) so the transport shape lives in one place.
func NewFastTransport() *notify.HTTPProvider {
	return &notify.HTTPProvider{
		Client:    &http.Client{Timeout: 2 * time.Second},
		Backoff:   NewFastBackoff(),
		UserAgent: "runwisp-notify/test",
	}
}

// NewTestRenderer builds a TemplateRenderer from the default template for the
// given channel kind. contentType varies by channel (e.g. "application/json"
// for slack/discord/webhook, "text/html" for telegram).
func NewTestRenderer(t *testing.T, kind, contentType string) render.Renderer {
	t.Helper()
	body, err := render.LoadDefaultTemplate(kind)
	require.NoError(t, err)
	r, err := render.NewTemplateRenderer(kind+":test", body, contentType, render.DefaultTitle)
	require.NoError(t, err)
	return r
}

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

// ManualTimers is a deterministic stand-in for time.AfterFunc. Each After call
// records the callback and hands back a *ManualTimer; the test fires callbacks
// explicitly via FireAll, so coalescing windows close without wall-clock waits.
type ManualTimers struct {
	mu     sync.Mutex
	timers []*ManualTimer
}

// ManualTimer is a cancellable handle returned by ManualTimers.After. It
// satisfies any { Stop() bool } seam (e.g. coalesce's timerHandle).
type ManualTimer struct {
	fn      func()
	stopped bool
}

// Stop marks the timer cancelled so FireAll skips it; it always reports true.
func (t *ManualTimer) Stop() bool {
	t.stopped = true
	return true
}

// Stopped reports whether Stop was called.
func (t *ManualTimer) Stopped() bool { return t.stopped }

func NewManualTimers() *ManualTimers { return &ManualTimers{} }

// After records fn and returns its handle. Matches a
// `func(time.Duration, func()) <handle>` seam; the duration is ignored.
func (m *ManualTimers) After(_ time.Duration, fn func()) *ManualTimer {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &ManualTimer{fn: fn}
	m.timers = append(m.timers, t)
	return t
}

// FireAll invokes every not-yet-stopped callback in registration order. Each
// fn runs synchronously on the caller's goroutine.
func (m *ManualTimers) FireAll() {
	m.mu.Lock()
	timers := append([]*ManualTimer(nil), m.timers...)
	m.mu.Unlock()
	for _, t := range timers {
		if !t.stopped {
			t.fn()
		}
	}
}

// Pending counts scheduled, not-yet-stopped callbacks.
func (m *ManualTimers) Pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.timers {
		if !t.stopped {
			n++
		}
	}
	return n
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
