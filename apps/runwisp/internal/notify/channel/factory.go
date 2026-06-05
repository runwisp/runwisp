// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package channel groups outbound notify channel implementations and the
// build-from-spec factory. The factory lives here (not in the notify
// package) because the notify package is imported by every provider
// sub-package — putting the factory upstream would create an import cycle.
package channel

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/discord"
	"github.com/runwisp/runwisp/internal/notify/channel/slack"
	"github.com/runwisp/runwisp/internal/notify/channel/smtp"
	"github.com/runwisp/runwisp/internal/notify/channel/telegram"
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// NotifierSpec is the resolved-from-TOML, secret-substituted description of a
// single [[notifier]]. The factory produces one Channel per spec; specs are
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

	// Webhook-specific
	URL     string
	Headers map[string]string

	TemplatePath string // optional override
	// Transport overrides the channel's HTTP transport. Nil means use defaults.
	// Daemon-level glue uses this to apply a global backoff override on HTTP
	// providers (Slack, Telegram). SMTP, which does not speak HTTP, reads its
	// retry settings from Backoff instead.
	Transport *notify.HTTPProvider
	// Backoff overrides the retry policy for non-HTTP providers (SMTP) and is
	// the source of truth used to populate Transport when none is supplied for
	// the HTTP providers. Zero means use DefaultBackoff.
	Backoff notify.BackoffConfig
	// RenderContext binds per-daemon values (external URL, fingerprint, tail
	// reader) into the template's func map. Zero values produce safe defaults:
	// missing external URL collapses link blocks, missing fingerprint shortens
	// the footer, missing tail reader collapses the output-tail blocks.
	RenderContext render.TemplateContext
}

// Build turns a NotifierSpec into a notify.Channel. Inapp is built separately
// by the Service since it needs the Coalescer/Hub deps.
func Build(spec NotifierSpec) (notify.Channel, error) {
	switch spec.Type {
	case "slack":
		body, err := render.LoadTemplate("slack", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRendererWithContext("slack:"+spec.ID, body, "application/json", render.DefaultTitle, spec.RenderContext)
		if err != nil {
			return nil, err
		}
		return slack.New(slack.Config{
			ID:         spec.ID,
			WebhookURL: spec.WebhookURL,
			Channel:    spec.SlackChannel,
			Renderer:   r,
			Transport:  httpTransport(spec),
		})
	case "discord":
		body, err := render.LoadTemplate("discord", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRendererWithContext("discord:"+spec.ID, body, "application/json", render.DefaultTitle, spec.RenderContext)
		if err != nil {
			return nil, err
		}
		return discord.New(discord.Config{
			ID:         spec.ID,
			WebhookURL: spec.WebhookURL,
			Renderer:   r,
			Transport:  httpTransport(spec),
		})
	case "telegram":
		body, err := render.LoadTemplate("telegram", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRendererWithContext("telegram:"+spec.ID, body, "text/html", render.DefaultTitle, spec.RenderContext)
		if err != nil {
			return nil, err
		}
		return telegram.New(telegram.Config{
			ID:        spec.ID,
			BotToken:  spec.BotToken,
			ChatID:    spec.ChatID,
			ParseMode: spec.ParseMode,
			Renderer:  r,
			Transport: httpTransport(spec),
		})
	case "smtp":
		body, err := render.LoadTemplate("smtp", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRendererWithContext("smtp:"+spec.ID, body, "text/html", render.DefaultTitle, spec.RenderContext)
		if err != nil {
			return nil, err
		}
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
	case "webhook":
		body, err := render.LoadTemplate("webhook", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRendererWithContext("webhook:"+spec.ID, body, "application/json", render.DefaultTitle, spec.RenderContext)
		if err != nil {
			return nil, err
		}
		return webhook.New(webhook.Config{
			ID:        spec.ID,
			URL:       spec.URL,
			Headers:   spec.Headers,
			Renderer:  r,
			Transport: httpTransport(spec),
		})
	default:
		return nil, fmt.Errorf("unknown notifier type %q (id=%s)", spec.Type, spec.ID)
	}
}

// httpTransport resolves the HTTP transport used by Slack/Telegram channels.
// Precedence: a caller-supplied spec.Transport wins (the daemon constructs it
// when it needs full control over the HTTPDoer). Otherwise, if spec.Backoff
// is set, build a default HTTPProvider with that backoff applied; nil means
// "let the channel construct its own defaults".
func httpTransport(spec NotifierSpec) *notify.HTTPProvider {
	if spec.Transport != nil {
		return spec.Transport
	}
	if spec.Backoff.IsZero() {
		return nil
	}
	p := notify.NewHTTPProvider()
	p.Backoff = spec.Backoff
	return p
}
