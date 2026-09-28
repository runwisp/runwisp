// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package ntfy implements the ntfy push notify channel. ntfy accepts a JSON
// publish POSTed to the server root, so the channel is the generic webhook
// channel with an ntfy-shaped default template, the topic injected into the
// body, and an optional access token sent as a bearer header. The token is
// secret; it is resolved at config load.
package ntfy

import (
	"strings"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// DefaultURL is the public ntfy server, used when the notifier sets no url.
const DefaultURL = "https://ntfy.sh"

// Config is the inputs the factory needs to build an ntfy channel.
type Config struct {
	ID        string
	URL       string // server base URL; empty means DefaultURL
	Topic     string
	Token     string // optional access token
	Renderer  render.Renderer
	Transport *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs an ntfy channel. Delivery is delegated to the webhook
// channel.
func New(cfg Config) (notify.Channel, error) {
	base := cfg.URL
	if base == "" {
		base = DefaultURL
	}
	var headers map[string]string
	if cfg.Token != "" {
		headers = map[string]string{"Authorization": "Bearer " + cfg.Token}
	}
	return webhook.New(webhook.Config{
		Kind:      "ntfy",
		ID:        cfg.ID,
		URL:       strings.TrimRight(base, "/"),
		Headers:   headers,
		Renderer:  cfg.Renderer,
		Transport: cfg.Transport,
		Fields:    map[string]string{"topic": cfg.Topic},
	})
}
