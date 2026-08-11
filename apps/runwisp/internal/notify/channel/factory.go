// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package channel groups outbound notify channel implementations and the
// build-from-spec factory. The factory lives here (not in the notify
// package) because the notify package is imported by every provider
// sub-package — putting the factory upstream would create an import cycle.
package channel

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel/discord"
	"github.com/runwisp/runwisp/internal/notify/channel/sendmail"
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

	// sendmail-specific: an explicit MTA binary. Empty means "find the system
	// one". From/Recipients/CC/BCC are shared with SMTP.
	SendmailPath string

	// Webhook-specific
	URL     string
	Headers map[string]string

	TemplatePath string // optional override
	// Transport overrides the channel's HTTP transport. Nil means use defaults.
	// Daemon-level glue uses this to apply a global backoff override on HTTP
	// providers (Slack, Telegram).
	Transport *notify.HTTPProvider
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
		return buildSlack(spec)
	case "discord":
		return buildDiscord(spec)
	case "telegram":
		return buildTelegram(spec)
	case "smtp":
		return buildSMTP(spec)
	case "sendmail":
		return buildSendmail(spec)
	case "webhook":
		return buildWebhook(spec)
	default:
		return nil, fmt.Errorf("unknown notifier type %q (id=%s)", spec.Type, spec.ID)
	}
}

// renderer loads the channel's template and builds a renderer keyed by
// "<type>:<id>", using contentType for the rendered payload's MIME type.
func renderer(spec NotifierSpec, contentType string) (*render.TemplateRenderer, error) {
	body, err := render.LoadTemplate(spec.Type, spec.TemplatePath)
	if err != nil {
		return nil, err
	}
	return render.NewTemplateRendererWithContext(spec.Type+":"+spec.ID, body, contentType, render.DefaultTitle, spec.RenderContext)
}

func buildSlack(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "application/json")
	if err != nil {
		return nil, err
	}
	return slack.New(slack.Config{
		ID:         spec.ID,
		WebhookURL: spec.WebhookURL,
		Channel:    spec.SlackChannel,
		Renderer:   r,
		Transport:  spec.Transport,
	})
}

func buildDiscord(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "application/json")
	if err != nil {
		return nil, err
	}
	return discord.New(discord.Config{
		ID:         spec.ID,
		WebhookURL: spec.WebhookURL,
		Renderer:   r,
		Transport:  spec.Transport,
	})
}

func buildTelegram(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "text/html")
	if err != nil {
		return nil, err
	}
	return telegram.New(telegram.Config{
		ID:        spec.ID,
		BotToken:  spec.BotToken,
		ChatID:    spec.ChatID,
		ParseMode: spec.ParseMode,
		Renderer:  r,
		Transport: spec.Transport,
	})
}

func buildSMTP(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "text/html")
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
		Renderer:      r,
	})
}

func buildSendmail(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "text/plain")
	if err != nil {
		return nil, err
	}
	return sendmail.New(sendmail.Config{
		ID:         spec.ID,
		Path:       spec.SendmailPath,
		From:       spec.From,
		ReplyTo:    spec.ReplyTo,
		Recipients: spec.Recipients,
		CC:         spec.CC,
		BCC:        spec.BCC,
		Renderer:   r,
	})
}

func buildWebhook(spec NotifierSpec) (notify.Channel, error) {
	r, err := renderer(spec, "application/json")
	if err != nil {
		return nil, err
	}
	return webhook.New(webhook.Config{
		ID:        spec.ID,
		URL:       spec.URL,
		Headers:   spec.Headers,
		Renderer:  r,
		Transport: spec.Transport,
	})
}
