// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package pushover implements the Pushover push notify channel. The Pushover
// messages API accepts a JSON body carrying the application token and the
// user (or group) key, so the channel is the generic webhook channel with a
// Pushover-shaped default template and both keys injected into the body.
// Both keys are secret; they are resolved at config load.
package pushover

import (
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// Endpoint is the Pushover messages API.
const Endpoint = "https://api.pushover.net/1/messages.json"

// Config is the inputs the factory needs to build a Pushover channel.
type Config struct {
	ID        string
	Token     string // application token
	User      string // user or group key
	Renderer  render.Renderer
	Transport *notify.HTTPProvider // optional; default constructed when nil
	// Endpoint overrides the API URL. Tests only; empty means Endpoint.
	Endpoint string
}

// New constructs a Pushover channel. Delivery is delegated to the webhook
// channel.
func New(cfg Config) (notify.Channel, error) {
	url := cfg.Endpoint
	if url == "" {
		url = Endpoint
	}
	return webhook.New(webhook.Config{
		Kind:      "pushover",
		ID:        cfg.ID,
		URL:       url,
		Renderer:  cfg.Renderer,
		Transport: cfg.Transport,
		Fields:    map[string]string{"token": cfg.Token, "user": cfg.User},
	})
}
