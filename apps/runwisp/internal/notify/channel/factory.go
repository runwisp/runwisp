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
	"github.com/runwisp/runwisp/internal/notify/channel/slack"
	"github.com/runwisp/runwisp/internal/notify/channel/telegram"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// NotifierSpec is the resolved-from-TOML, secret-substituted description of a
// single [[notifier]]. The factory produces one Channel per spec; specs are
// supplied by the configload package.
type NotifierSpec struct {
	ID           string
	Type         string
	WebhookURL   string // slack
	BotToken     string // telegram
	ChatID       string // telegram
	ParseMode    string // telegram
	TemplatePath string // optional override
	// Transport overrides the channel's HTTP transport. Nil means use defaults.
	// Daemon-level glue uses this to apply a global backoff override.
	Transport *notify.HTTPProvider
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
		r, err := render.NewTemplateRenderer("slack:"+spec.ID, body, "application/json", render.DefaultTitle)
		if err != nil {
			return nil, err
		}
		return slack.New(slack.Config{
			ID:         spec.ID,
			WebhookURL: spec.WebhookURL,
			Renderer:   r,
			Transport:  spec.Transport,
		})
	case "telegram":
		body, err := render.LoadTemplate("telegram", spec.TemplatePath)
		if err != nil {
			return nil, err
		}
		r, err := render.NewTemplateRenderer("telegram:"+spec.ID, body, "text/html", render.DefaultTitle)
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
	default:
		return nil, fmt.Errorf("unknown notifier type %q (id=%s)", spec.Type, spec.ID)
	}
}
