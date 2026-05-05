// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"context"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Channel is the in-app sink: it lives in-process, talks to a Coalescer for
// dedupe + persistence, and broadcasts on a Hub for SSE consumers.
type Channel struct {
	id        string
	renderer  render.Renderer
	coalescer *Coalescer
}

// New constructs the in-app channel with id "inapp".
func New(id string, renderer render.Renderer, coalescer *Coalescer) *Channel {
	return &Channel{id: id, renderer: renderer, coalescer: coalescer}
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Match(*notify.Event) bool    { return true }
func (c *Channel) Close(context.Context) error { return nil }

// Execute renders the event and hands it to the Coalescer. The Coalescer
// owns mutex + index + hub fan-out; we just glue.
func (c *Channel) Execute(_ context.Context, ev *notify.Event) error {
	if ev == nil {
		return nil
	}
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return err
	}
	title := rendered.Title
	if title == "" {
		title = render.DefaultTitle(ev)
	}
	c.coalescer.Receive(title, string(rendered.Body), ev)
	return nil
}

// IngestSynthetic implements notify.FailureSink so the dispatcher can deliver
// delivery-failure events directly to the in-app sink, bypassing the router
// (cycle guard).
func (c *Channel) IngestSynthetic(ev *notify.Event) {
	_ = c.Execute(context.Background(), ev)
}
