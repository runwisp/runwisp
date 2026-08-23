// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package slack implements the Slack incoming-webhook notify channel. The
// webhook URL is a secret, resolved at config load. This file knows nothing
// about how the secret was obtained — it just consumes the resolved string.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Channel is a Slack webhook notifier.
type Channel struct {
	id         string
	webhookURL string
	channel    string
	transport  *notify.HTTPProvider
	renderer   render.Renderer
}

// Config is the inputs the factory needs to build a Slack Channel.
type Config struct {
	ID         string
	WebhookURL string
	Channel    string // optional Slack channel override (e.g. "#ops")
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
		channel:    cfg.Channel,
		transport:  transport,
		renderer:   cfg.Renderer,
	}, nil
}

func (c *Channel) ID() string                  { return c.id }
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
	body := rendered.Body
	if c.channel != "" {
		body, err = injectChannel(body, c.channel)
		if err != nil {
			return fmt.Errorf("%s: inject channel: %w", c, err)
		}
	}
	if err := c.transport.PostJSON(ctx, c.webhookURL, "application/json", body); err != nil {
		return fmt.Errorf("%s: %w", c, notify.RedactError(err, c.webhookURL))
	}
	return nil
}

// injectChannel adds a top-level "channel" key to a JSON object body.
func injectChannel(body []byte, ch string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	enc, _ := json.Marshal(ch)
	obj["channel"] = enc
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
