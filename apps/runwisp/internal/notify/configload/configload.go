// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package configload turns a parsed config.NotifyConfig into the runtime
// objects the notify.Service needs: secret-resolved channel.NotifierSpec
// values and compiled notify.Rule predicates. The split exists so the
// internal/config package stays free of secret I/O and runtime types.
package configload

import (
	"fmt"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/runwisp/runwisp/internal/secretref"
)

// ResolvedNotify carries everything the daemon needs to construct a
// notify.Service: secret-resolved notifier specs and compiled routing rules.
type ResolvedNotify struct {
	Notifiers []channel.NotifierSpec
	Rules     []notify.Rule
}

// Resolve takes the post-parse config and the daemon's data dir, reads any
// file-backed secrets relative to the data dir, and produces a ready-to-use
// ResolvedNotify. The renderCtx carries per-daemon values (external URL,
// fingerprint, output-tail reader) that bind into each channel's template
// func map. Returns an error when a secret source is missing or when a
// route refers to an unknown notifier.
func Resolve(cfg config.NotifyConfig, dataDir string, renderCtx render.TemplateContext) (ResolvedNotify, error) {
	specs := make([]channel.NotifierSpec, 0, len(cfg.Notifiers))
	for _, n := range cfg.Notifiers {
		spec, err := resolveNotifier(n, dataDir, renderCtx)
		if err != nil {
			return ResolvedNotify{}, err
		}
		specs = append(specs, spec)
	}

	rules := make([]notify.Rule, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		rule, err := compileRoute(r)
		if err != nil {
			return ResolvedNotify{}, err
		}
		rules = append(rules, rule)
	}

	return ResolvedNotify{
		Notifiers: specs,
		Rules:     rules,
	}, nil
}

func resolveNotifier(n config.NotifierSpec, dataDir string, renderCtx render.TemplateContext) (channel.NotifierSpec, error) {
	spec := channel.NotifierSpec{
		ID:            n.ID,
		Type:          n.Type,
		ParseMode:     n.ParseMode,
		ChatID:        n.ChatID,
		TemplatePath:  n.TemplatePath,
		RenderContext: renderCtx,
	}
	switch n.Type {
	case "slack":
		url, _, err := secretref.Resolve(n.WebhookURL, dataDir)
		if err != nil {
			return channel.NotifierSpec{}, fmt.Errorf("notifier %q webhook_url: %w", n.ID, err)
		}
		spec.WebhookURL = url
		spec.SlackChannel = n.SlackChannel
	case "telegram":
		token, _, err := secretref.Resolve(n.BotToken, dataDir)
		if err != nil {
			return channel.NotifierSpec{}, fmt.Errorf("notifier %q bot_token: %w", n.ID, err)
		}
		spec.BotToken = token
	case "smtp":
		if err := fillSMTPSpec(&spec, n, dataDir); err != nil {
			return channel.NotifierSpec{}, err
		}
	}
	return spec, nil
}

func fillSMTPSpec(spec *channel.NotifierSpec, n config.NotifierSpec, dataDir string) error {
	spec.Host = n.Host
	spec.Port = n.Port
	spec.TLSMode = n.TLSMode
	spec.TLSSkipVerify = n.TLSSkipVerify
	spec.Username = n.Username
	spec.From = n.From
	spec.ReplyTo = n.ReplyTo
	spec.Recipients = append([]string(nil), n.Recipients...)
	spec.CC = append([]string(nil), n.CC...)
	spec.BCC = append([]string(nil), n.BCC...)
	if strings.TrimSpace(n.Password) == "" {
		// Auth-less local relay (e.g. Postfix on 127.0.0.1:25); leave empty.
		return nil
	}
	pw, _, err := secretref.Resolve(n.Password, dataDir)
	if err != nil {
		return fmt.Errorf("notifier %q password: %w", n.ID, err)
	}
	spec.Password = pw
	return nil
}

func compileRoute(r config.NotificationRoute) (notify.Rule, error) {
	preds := make([]notify.Predicate, 0, 3)
	if len(r.Kinds) > 0 {
		kinds := make([]notify.Kind, len(r.Kinds))
		for i, k := range r.Kinds {
			kinds[i] = notify.Kind(k)
		}
		preds = append(preds, notify.MatchKind(kinds...))
	}
	if r.Severity != "" {
		preds = append(preds, notify.MatchSeverity(notify.Severity(r.Severity)))
	}
	if r.TaskGlob != "" {
		preds = append(preds, notify.MatchTaskGlob(r.TaskGlob))
	}
	return notify.Rule{
		Match:     notify.And(preds...),
		ActionIDs: append([]string(nil), r.NotifierID...),
	}, nil
}
