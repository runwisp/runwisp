// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package webhook implements a generic HTTP webhook notify channel. The
// operator supplies a URL and optional custom headers; RunWisp POSTs a
// JSON body rendered from the event template on each notification.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
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
	fields    map[string]string
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
	// Fields are top-level string keys set on the rendered JSON object
	// before it is POSTed, overriding any the template wrote (e.g. Slack's
	// target channel, ntfy's topic, Pushover's credentials). Keeping them out
	// of the template means a template_path override can't drop them.
	Fields map[string]string
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
		fields:    cfg.Fields,
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
	body := rendered.Body
	if len(c.fields) > 0 {
		body, err = injectFields(body, c.fields)
		if err != nil {
			return fmt.Errorf("%s: inject fields: %w", c, err)
		}
	}
	if err := c.transport.PostJSONWithHeaders(ctx, c.url, "application/json", body, c.headers); err != nil {
		return fmt.Errorf("%s: %w", c, notify.RedactError(err, c.url))
	}
	return nil
}

// injectFields sets top-level string keys on a JSON object body.
func injectFields(body []byte, fields map[string]string) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	for k, v := range fields {
		enc, _ := json.Marshal(v)
		obj[k] = enc
	}
	var buf bytes.Buffer
	e := json.NewEncoder(&buf)
	e.SetEscapeHTML(false)
	if err := e.Encode(obj); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
