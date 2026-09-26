// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package slack implements the Slack incoming-webhook notify channel. A Slack
// webhook is a plain HTTP endpoint that accepts a JSON body, so the channel is
// the generic webhook channel with a Slack-shaped default template and an
// optional top-level "channel" key injected into the body. The webhook URL is
// secret; it is resolved at config load.
package slack

import (
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Config is the inputs the factory needs to build a Slack channel.
type Config struct {
	ID         string
	WebhookURL string
	Channel    string // optional Slack channel override (e.g. "#ops")
	Renderer   render.Renderer
	Transport  *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs a Slack channel. Delivery (POST JSON, backoff, secret
// redaction) is delegated to the webhook channel; only the optional channel
// override is Slack-specific, injected as a top-level body field.
func New(cfg Config) (notify.Channel, error) {
	var fields map[string]string
	if cfg.Channel != "" {
		fields = map[string]string{"channel": cfg.Channel}
	}
	return webhook.New(webhook.Config{
		Kind:      "slack",
		ID:        cfg.ID,
		URL:       cfg.WebhookURL,
		Renderer:  cfg.Renderer,
		Transport: cfg.Transport,
		Fields:    fields,
	})
}
