// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package telegram implements the Telegram bot notify channel. Bot token is
// secret; chat_id is not. Token is resolved at config load.
package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

const apiBase = "https://api.telegram.org"

// Channel is a Telegram bot notifier.
type Channel struct {
	id        string
	botToken  string
	chatID    string
	parseMode string
	apiBase   string
	transport *notify.HTTPProvider
	renderer  render.Renderer
}

// Config is the inputs the factory needs to build a Telegram Channel.
type Config struct {
	ID        string
	BotToken  string
	ChatID    string
	ParseMode string // "HTML" | "MarkdownV2"; defaults to HTML
	Renderer  render.Renderer
	Transport *notify.HTTPProvider
	APIBase   string // override for tests
}

// New constructs a Telegram channel.
func New(cfg Config) (*Channel, error) {
	if cfg.BotToken == "" {
		return nil, fmt.Errorf("telegram channel %q: bot token is required", cfg.ID)
	}
	if cfg.ChatID == "" {
		return nil, fmt.Errorf("telegram channel %q: chat_id is required", cfg.ID)
	}
	if cfg.Renderer == nil {
		return nil, fmt.Errorf("telegram channel %q: renderer is required", cfg.ID)
	}
	transport := cfg.Transport
	if transport == nil {
		transport = notify.NewHTTPProvider()
		transport.Body429Fn = parseTelegramRetryAfter
	} else if transport.Body429Fn == nil {
		transport.Body429Fn = parseTelegramRetryAfter
	}
	parse := strings.TrimSpace(cfg.ParseMode)
	if parse == "" {
		parse = "HTML"
	}
	api := strings.TrimSpace(cfg.APIBase)
	if api == "" {
		api = apiBase
	}
	return &Channel{
		id:        cfg.ID,
		botToken:  cfg.BotToken,
		chatID:    cfg.ChatID,
		parseMode: parse,
		apiBase:   api,
		transport: transport,
		renderer:  cfg.Renderer,
	}, nil
}

func (c *Channel) ID() string                  { return c.id }
func (c *Channel) Close(context.Context) error { return nil }
func (c *Channel) String() string              { return "telegram:" + c.id }

// Execute renders the event and POSTs to bot/sendMessage. The body uses
// application/x-www-form-urlencoded; that's the simplest form Telegram
// accepts for plain text + parse_mode.
func (c *Channel) Execute(ctx context.Context, ev *notify.Event) error {
	rendered, err := c.renderer.Render(ev)
	if err != nil {
		return fmt.Errorf("%s: render: %w", c, err)
	}
	form := url.Values{}
	form.Set("chat_id", c.chatID)
	form.Set("text", string(rendered.Body))
	form.Set("parse_mode", c.parseMode)

	endpoint := fmt.Sprintf("%s/bot%s/sendMessage", c.apiBase, c.botToken)
	if err := c.transport.PostJSON(ctx, endpoint, "application/x-www-form-urlencoded", []byte(form.Encode())); err != nil {
		return fmt.Errorf("%s: %s", c, notify.Redact(err.Error(), c.botToken))
	}
	return nil
}

// parseTelegramRetryAfter pulls parameters.retry_after out of a 429 body, in
// seconds. Returns 0 when the body isn't shaped like a Telegram 429.
func parseTelegramRetryAfter(body []byte) time.Duration {
	var resp struct {
		OK         bool `json:"ok"`
		Parameters struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0
	}
	if resp.Parameters.RetryAfter > 0 {
		return time.Duration(resp.Parameters.RetryAfter) * time.Second
	}
	return 0
}
