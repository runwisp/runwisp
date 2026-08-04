// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package configload turns a parsed config.NotifyConfig into the runtime
// objects the notify.Service needs: channel.NotifierSpec values and compiled
// notify.Rule predicates. The split exists so the internal/config package
// stays free of runtime types.
package configload

import (
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/render"
)

// ResolvedNotify carries everything the daemon needs to construct a
// notify.Service: notifier specs and compiled routing rules.
type ResolvedNotify struct {
	Notifiers []channel.NotifierSpec
	Rules     []notify.Rule
}

// Resolve takes the post-parse config and produces a ready-to-use
// ResolvedNotify. Secret values (webhook URLs, tokens, passwords) arrive
// final — ${VAR} / ${file:...} substitution already ran at config load. The
// renderCtx carries per-daemon values (external URL, fingerprint, output-tail
// reader) that bind into each channel's template func map. Returns an error
// when a route refers to an unknown notifier.
func Resolve(cfg config.NotifyConfig, renderCtx render.TemplateContext) (ResolvedNotify, error) {
	specs := make([]channel.NotifierSpec, 0, len(cfg.Notifiers))
	for _, n := range cfg.Notifiers {
		specs = append(specs, resolveNotifier(n, renderCtx))
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

func resolveNotifier(n config.NotifierSpec, renderCtx render.TemplateContext) channel.NotifierSpec {
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
		spec.WebhookURL = n.WebhookURL
		spec.SlackChannel = n.SlackChannel
	case "discord":
		spec.WebhookURL = n.WebhookURL
	case "telegram":
		spec.BotToken = n.BotToken
	case "smtp":
		fillSMTPSpec(&spec, n)
	case "sendmail":
		fillSendmailSpec(&spec, n)
	case "webhook":
		spec.URL = n.URL
		if n.Headers != nil {
			h := make(map[string]string, len(n.Headers))
			for k, v := range n.Headers {
				h[k] = v
			}
			spec.Headers = h
		}
	}
	return spec
}

// fillSendmailSpec copies the addressing a local-MTA notifier needs. It shares
// From/To/CC/BCC with SMTP and takes none of the relay settings: the MTA
// already knows where to relay, which is the point of using it.
func fillSendmailSpec(spec *channel.NotifierSpec, n config.NotifierSpec) {
	spec.SendmailPath = n.SendmailPath
	spec.From = n.From
	spec.ReplyTo = n.ReplyTo
	spec.Recipients = append([]string(nil), n.Recipients...)
	spec.CC = append([]string(nil), n.CC...)
	spec.BCC = append([]string(nil), n.BCC...)
}

func fillSMTPSpec(spec *channel.NotifierSpec, n config.NotifierSpec) {
	spec.Host = n.Host
	spec.Port = n.Port
	spec.TLSMode = n.TLSMode
	spec.TLSSkipVerify = n.TLSSkipVerify
	spec.Username = n.Username
	// Empty Password means an auth-less local relay (e.g. Postfix on
	// 127.0.0.1:25); validation guarantees username/password come together.
	spec.Password = n.Password
	spec.From = n.From
	spec.ReplyTo = n.ReplyTo
	spec.Recipients = append([]string(nil), n.Recipients...)
	spec.CC = append([]string(nil), n.CC...)
	spec.BCC = append([]string(nil), n.BCC...)
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
