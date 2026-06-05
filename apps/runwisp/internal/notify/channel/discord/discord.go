// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package discord implements the Discord notify channel. A Discord webhook
// is a plain HTTP endpoint that accepts a JSON body, so the channel is the
// generic webhook channel with a Discord-shaped default template and a
// Discord-aware 429 parser layered on. The webhook URL is secret; it is
// resolved at config load.
package discord

import (
	"encoding/json"
	"math"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Config is the inputs the factory needs to build a Discord channel.
type Config struct {
	ID         string
	WebhookURL string
	Renderer   render.Renderer
	Transport  *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs a Discord channel. Delivery (POST JSON, backoff, secret
// redaction) is delegated to the webhook channel; only the rate-limit body
// parsing is Discord-specific.
func New(cfg Config) (notify.Channel, error) {
	transport := cfg.Transport
	if transport == nil {
		transport = notify.NewHTTPProvider()
	}
	if transport.Body429Fn == nil {
		transport.Body429Fn = parseRetryAfter
	}
	return webhook.New(webhook.Config{
		Kind:      "discord",
		ID:        cfg.ID,
		URL:       cfg.WebhookURL,
		Renderer:  cfg.Renderer,
		Transport: transport,
	})
}

// parseRetryAfter pulls retry_after (seconds, with millisecond precision)
// out of a Discord 429 body. Returns 0 when the body isn't shaped like a
// Discord 429.
func parseRetryAfter(body []byte) time.Duration {
	var resp struct {
		RetryAfter float64 `json:"retry_after"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0
	}
	if resp.RetryAfter <= 0 {
		return 0
	}
	return time.Duration(math.Round(resp.RetryAfter*1000)) * time.Millisecond
}
