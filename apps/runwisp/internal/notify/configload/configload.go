// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package configload turns a parsed config.NotifyConfig into the runtime
// objects the notify.Service needs: channel.NotifierSpec values and compiled
// notify.Rule predicates. The split exists so the internal/config package
// stays free of runtime types.
package configload

import (
	"slices"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/render"
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
// reader) that bind into each channel's template func map. Route-to-notifier
// references were already validated at config load.
func Resolve(cfg config.NotifyConfig, renderCtx render.TemplateContext) ResolvedNotify {
	specs := make([]channel.NotifierSpec, 0, len(cfg.Notifiers))
	for _, n := range cfg.Notifiers {
		specs = append(specs, channel.NotifierSpec{NotifierSpec: n, RenderContext: renderCtx})
	}

	rules := make([]notify.Rule, 0, len(cfg.Routes))
	for _, r := range cfg.Routes {
		rules = append(rules, compileRoute(r))
	}

	return ResolvedNotify{
		Notifiers: specs,
		Rules:     rules,
	}
}

func compileRoute(r config.NotificationRoute) notify.Rule {
	preds := make([]notify.Predicate, 0, 3)
	if r.MatchFailure {
		preds = append(preds, notify.MatchFailure())
	}
	if len(r.Kinds) > 0 {
		preds = append(preds, notify.MatchOutcomes(r.Kinds...))
	}
	if r.TaskGlob != "" {
		preds = append(preds, notify.MatchTaskGlob(r.TaskGlob))
	}
	return notify.Rule{
		Match:     notify.And(preds...),
		ActionIDs: slices.Clone(r.NotifierID),
	}
}
