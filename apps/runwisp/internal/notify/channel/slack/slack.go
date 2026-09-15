// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package slack implements the Slack incoming-webhook notify channel. A Slack
// webhook is a plain HTTP endpoint that accepts a JSON body, so the channel is
// the generic webhook channel with a Slack-shaped default template and an
// optional top-level "channel" key injected into the body. The webhook URL is
// secret; it is resolved at config load.
package slack

import (
	"bytes"
	"encoding/json"

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
// override is Slack-specific, applied as a body transform.
func New(cfg Config) (notify.Channel, error) {
	var transform func([]byte) ([]byte, error)
	if cfg.Channel != "" {
		ch := cfg.Channel
		transform = func(body []byte) ([]byte, error) { return injectChannel(body, ch) }
	}
	return webhook.New(webhook.Config{
		Kind:      "slack",
		ID:        cfg.ID,
		URL:       cfg.WebhookURL,
		Renderer:  cfg.Renderer,
		Transport: cfg.Transport,
		Transform: transform,
	})
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
