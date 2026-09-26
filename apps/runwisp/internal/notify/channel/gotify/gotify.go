// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package gotify implements the Gotify push notify channel. Gotify accepts a
// JSON message POSTed to <server>/message, authenticated by an application
// token header, so the channel is the generic webhook channel with a
// Gotify-shaped default template. The token is secret; it is resolved at
// config load.
package gotify

import (
	"strings"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Config is the inputs the factory needs to build a Gotify channel.
type Config struct {
	ID        string
	URL       string // server base URL
	Token     string // application token
	Renderer  render.Renderer
	Transport *notify.HTTPProvider // optional; default constructed when nil
}

// New constructs a Gotify channel. Delivery is delegated to the webhook
// channel.
func New(cfg Config) (notify.Channel, error) {
	url := ""
	if cfg.URL != "" {
		url = strings.TrimRight(cfg.URL, "/") + "/message"
	}
	return webhook.New(webhook.Config{
		Kind:      "gotify",
		ID:        cfg.ID,
		URL:       url,
		Headers:   map[string]string{"X-Gotify-Key": cfg.Token},
		Renderer:  cfg.Renderer,
		Transport: cfg.Transport,
	})
}
