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

	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/render"
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
// fast test backoff. Shared by every HTTP-backed channel test so the
// transport shape lives in one place.
func NewFastTransport() *notify.HTTPProvider {
	return &notify.HTTPProvider{
		Client:    &http.Client{Timeout: 2 * time.Second},
		Backoff:   NewFastBackoff(),
		UserAgent: "runwisp-notify/test",
	}
}

// NewTestRenderer builds a TemplateRenderer from the default template for the
// given channel kind.
func NewTestRenderer(t *testing.T, kind string) render.Renderer {
	t.Helper()
	body, err := render.LoadDefaultTemplate(kind)
	require.NoError(t, err)
	r, err := render.NewTemplateRenderer(kind+":test", body, render.DefaultTitle, render.TemplateContext{})
	require.NoError(t, err)
	return r
}

// FakeChannel records every Execute call. Optionally returns a configured
// error to simulate transient or permanent failures.
type FakeChannel struct {
	IDValue string
	Err     error

	mu       sync.Mutex
	received []*notify.Event
	closed   bool
}

func NewFakeChannel(id string) *FakeChannel {
	return &FakeChannel{IDValue: id}
}

func (f *FakeChannel) ID() string { return f.IDValue }

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

func (f *FakeChannel) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
