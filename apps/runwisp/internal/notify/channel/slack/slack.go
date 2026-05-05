// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package slack implements the Slack incoming-webhook notify channel. The
// webhook URL is a secret, resolved at config load. This file knows nothing
// about how the secret was obtained — it just consumes the resolved string.
package slack

import (
	"context"
	"fmt"
	"strings"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Channel is a Slack webhook notifier.
type Channel struct {
	id         string
	webhookURL string
	transport  *notify.HTTPProvider
	renderer   render.Renderer
}

// Config is the inputs the factory needs to build a Slack Channel.
type Config struct {
	ID         string
	WebhookURL string
	Renderer   render.Renderer
	Transport  *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs a Slack channel.
func New(cfg Config) (*Channel, error) {
	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("slack channel %q: webhook URL is required", cfg.ID)
	}
	if cfg.Renderer == nil {
		return nil, fmt.Errorf("slack channel %q: renderer is required", cfg.ID)
	}
	transport := cfg.Transport
	if transport == nil {
		transport = notify.NewHTTPProvider()
	}
	return &Channel{
		id:         cfg.ID,
		webhookURL: cfg.WebhookURL,
		transport:  transport,
		renderer:   cfg.Renderer,
	}, nil
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Match(*notify.Event) bool    { return true }
func (c *Channel) Close(context.Context) error { return nil }

// String produces a log-friendly type-prefixed identifier. This is the only
// safe representation for error wrapping; channel.webhookURL never appears.
func (c *Channel) String() string { return "slack:" + c.id }

// Execute renders the event and POSTs the JSON body to the webhook.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return fmt.Errorf("%s: render: %w", c, err)
	}
	if err := c.transport.PostJSON(ctx, c.webhookURL, "application/json", rendered.Body); err != nil {
		return fmt.Errorf("%s: %s", c, c.redact(err.Error()))
	}
	return nil
}

// redact strips the webhook URL out of any string. Net/http embeds the full
// URL in *url.Error.String(); without this the secret leaks into in-app
// delivery-failure notifications.
func (c *Channel) redact(s string) string {
	if c.webhookURL == "" {
		return s
	}
	return strings.ReplaceAll(s, c.webhookURL, "[redacted]")
}
