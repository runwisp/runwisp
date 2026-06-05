// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package webhook implements a generic HTTP webhook notify channel. The
// operator supplies a URL and optional custom headers; RunWisp POSTs a
// JSON body rendered from the event template on each notification.
package webhook

import (
	"context"
	"fmt"
	"net/http"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Channel is a generic HTTP webhook notifier.
type Channel struct {
	kind      string
	id        string
	url       string
	headers   http.Header
	transport *notify.HTTPProvider
	renderer  render.Renderer
}

// Config is the inputs the factory needs to build a webhook Channel.
type Config struct {
	// Kind labels the channel in errors and logs (e.g. "discord" for
	// providers built on top of this one). Empty means "webhook".
	Kind      string
	ID        string
	URL       string
	Headers   map[string]string // optional; merged into every request
	Renderer  render.Renderer
	Transport *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs a webhook channel.
func New(cfg Config) (*Channel, error) {
	kind := cfg.Kind
	if kind == "" {
		kind = "webhook"
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("%s channel %q: url is required", kind, cfg.ID)
	}
	if cfg.Renderer == nil {
		return nil, fmt.Errorf("%s channel %q: renderer is required", kind, cfg.ID)
	}
	transport := cfg.Transport
	if transport == nil {
		transport = notify.NewHTTPProvider()
	}
	var h http.Header
	if len(cfg.Headers) > 0 {
		h = make(http.Header, len(cfg.Headers))
		for k, v := range cfg.Headers {
			h.Set(k, v)
		}
	}
	return &Channel{
		kind:      kind,
		id:        cfg.ID,
		url:       cfg.URL,
		headers:   h,
		transport: transport,
		renderer:  cfg.Renderer,
	}, nil
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Close(context.Context) error { return nil }

func (c *Channel) String() string { return c.kind + ":" + c.id }

// Execute renders the event and POSTs the JSON body to the configured URL.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return fmt.Errorf("%s: render: %w", c, err)
	}
	if err := c.transport.PostJSONWithHeaders(ctx, c.url, "application/json", rendered.Body, c.headers); err != nil {
		return fmt.Errorf("%s: %s", c, notify.Redact(err.Error(), c.url))
	}
	return nil
}
