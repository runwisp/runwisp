// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package channel groups outbound notify channel implementations and the
// build-from-spec factory. The factory lives here (not in the notify
// package) because the notify package is imported by every provider
// sub-package — putting the factory upstream would create an import cycle.
package channel

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/sendmail"
	"github.com/runwisp/runwisp/internal/notify/channel/smtp"
	"github.com/runwisp/runwisp/internal/notify/channel/telegram"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// NotifierSpec is the resolved-from-TOML, secret-substituted description of a
// single [notifiers.<id>]. The factory produces one Channel per spec; specs are
// supplied by the configload package.
type NotifierSpec struct {
	ID           string
	Type         string
	WebhookURL   string // slack, discord
	SlackChannel string // slack channel override (e.g. "#ops")
	BotToken     string // telegram
	ChatID       string // telegram
	ParseMode    string // telegram

	// SMTP-specific
	Host          string
	Port          int
	TLSMode       string
	TLSSkipVerify bool
	Username      string
	Password      string
	From          string
	ReplyTo       string
	Recipients    []string
	CC            []string
	BCC           []string

	// sendmail-specific: an explicit MTA binary. Empty means "find the system
	// one". From/Recipients/CC/BCC are shared with SMTP.
	SendmailPath string

	// Webhook-specific (URL is also the ntfy/gotify server base)
	URL     string
	Headers map[string]string

	// Push-specific (ntfy, gotify, pushover)
	Topic string // ntfy
	Token string // ntfy access token, gotify/pushover application token
	User  string // pushover user or group key

	// Template is the body of the operator's template_path override, read by
	// the caller; "" means the embedded default for Type.
	Template string
	// Transport overrides the channel's HTTP transport. Nil means use defaults.
	// Daemon-level glue uses this to apply a global backoff override on HTTP
	// providers (Slack, Discord, Telegram, ntfy, Gotify, Pushover, webhook).
	Transport *notify.HTTPProvider
	// Backoff overrides the channel's retry backoff for non-HTTP providers
	// (SMTP, sendmail), which don't go through Transport. Zero means use
	// notify.DefaultBackoff(). Daemon-level glue sets this from the same
	// retry_budget override applied to Transport above.
	Backoff notify.BackoffConfig
	// RenderContext binds per-daemon values (external URL, fingerprint, tail
	// reader) into the template's func map. Zero values produce safe defaults:
	// missing external URL collapses link blocks, missing fingerprint shortens
	// the footer, missing tail reader collapses the output-tail blocks.
	RenderContext render.TemplateContext
}

// contentTypes maps every supported notifier type to the MIME type of its
// rendered payload; a type missing here is unknown.
var contentTypes = map[string]string{
	"slack":    "application/json",
	"discord":  "application/json",
	"ntfy":     "application/json",
	"gotify":   "application/json",
	"pushover": "application/json",
	"webhook":  "application/json",
	"telegram": "text/html",
	"smtp":     "text/html",
	"sendmail": "text/plain",
}

// Build turns a NotifierSpec into a notify.Channel. Inapp is built separately
// since it needs the Coalescer/Hub deps.
func Build(spec NotifierSpec) (notify.Channel, error) {
	contentType, ok := contentTypes[spec.Type]
	if !ok {
		return nil, fmt.Errorf("unknown notifier type %q (id=%s)", spec.Type, spec.ID)
	}
	body := spec.Template
	if body == "" {
		var err error
		if body, err = render.LoadDefaultTemplate(spec.Type); err != nil {
			return nil, err
		}
	}
	r, err := render.NewTemplateRenderer(spec.Type+":"+spec.ID, body, contentType, render.DefaultTitle, spec.RenderContext)
	if err != nil {
		return nil, err
	}
	switch spec.Type {
	case "telegram":
		return telegram.New(telegram.Config{
			ID:        spec.ID,
			BotToken:  spec.BotToken,
			ChatID:    spec.ChatID,
			ParseMode: spec.ParseMode,
			Renderer:  r,
			Transport: spec.Transport,
		})
	case "smtp":
		return smtp.New(smtp.Config{
			ID:            spec.ID,
			Host:          spec.Host,
			Port:          spec.Port,
			TLSMode:       spec.TLSMode,
			TLSSkipVerify: spec.TLSSkipVerify,
			Username:      spec.Username,
			Password:      spec.Password,
			From:          spec.From,
			ReplyTo:       spec.ReplyTo,
			Recipients:    spec.Recipients,
			CC:            spec.CC,
			BCC:           spec.BCC,
			Backoff:       spec.Backoff,
			Renderer:      r,
		})
	case "sendmail":
		return sendmail.New(sendmail.Config{
			ID:         spec.ID,
			Path:       spec.SendmailPath,
			From:       spec.From,
			ReplyTo:    spec.ReplyTo,
			Recipients: spec.Recipients,
			CC:         spec.CC,
			BCC:        spec.BCC,
			Backoff:    spec.Backoff,
			Renderer:   r,
		})
	}
	cfg := webhookConfig(spec)
	cfg.Renderer = r
	return webhook.New(cfg)
}

// ntfyDefaultURL is the public ntfy server, used when the notifier sets no url.
const ntfyDefaultURL = "https://ntfy.sh"

// pushoverEndpoint is the Pushover messages API.
const pushoverEndpoint = "https://api.pushover.net/1/messages.json"

// webhookConfig maps the JSON-over-HTTP notifier types onto the generic
// webhook channel: each differs only in its URL, headers and the top-level
// body fields injected after rendering (kept out of the template so a
// template_path override can't drop them). Secrets (webhook URLs, tokens,
// keys) are resolved at config load.
func webhookConfig(spec NotifierSpec) webhook.Config {
	cfg := webhook.Config{Kind: spec.Type, ID: spec.ID, Transport: spec.Transport}
	switch spec.Type {
	case "slack":
		cfg.URL = spec.WebhookURL
		if spec.SlackChannel != "" {
			cfg.Fields = map[string]string{"channel": spec.SlackChannel}
		}
	case "discord":
		cfg.URL = spec.WebhookURL
		if cfg.Transport == nil {
			cfg.Transport = notify.NewHTTPProvider()
		}
		if cfg.Transport.Body429Fn == nil {
			cfg.Transport.Body429Fn = parseDiscordRetryAfter
		}
	case "ntfy":
		// JSON publishes go to the server root; the topic rides in the body.
		cfg.URL = strings.TrimRight(cmp.Or(spec.URL, ntfyDefaultURL), "/")
		if spec.Token != "" {
			cfg.Headers = map[string]string{"Authorization": "Bearer " + spec.Token}
		}
		cfg.Fields = map[string]string{"topic": spec.Topic}
	case "gotify":
		if spec.URL != "" {
			cfg.URL = strings.TrimRight(spec.URL, "/") + "/message"
		}
		cfg.Headers = map[string]string{"X-Gotify-Key": spec.Token}
	case "pushover":
		cfg.URL = pushoverEndpoint
		cfg.Fields = map[string]string{"token": spec.Token, "user": spec.User}
	default: // "webhook"
		cfg.URL = spec.URL
		cfg.Headers = spec.Headers
	}
	return cfg
}

// parseDiscordRetryAfter pulls retry_after (seconds, with millisecond
// precision) out of a Discord 429 body. Returns 0 when the body isn't shaped
// like a Discord 429.
func parseDiscordRetryAfter(body []byte) time.Duration {
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
