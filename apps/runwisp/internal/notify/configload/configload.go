// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package configload turns a parsed config.NotifyConfig into the runtime
// objects the notify.Service needs: secret-resolved channel.NotifierSpec
// values and compiled notify.Rule predicates. The split exists so the
// internal/config package stays free of secret I/O and runtime types.
package configload

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/render"
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
			url, err := resolveSecret(n.WebhookURL, n.WebhookURLEnv, n.WebhookURLFile, dataDir)
			if err != nil {
				return ResolvedNotify{}, fmt.Errorf("notifier %q webhook_url: %w", n.ID, err)
			}
			spec.WebhookURL = url
			spec.SlackChannel = n.SlackChannel
		case "telegram":
			token, err := resolveSecret(n.BotToken, n.BotTokenEnv, n.BotTokenFile, dataDir)
			if err != nil {
				return ResolvedNotify{}, fmt.Errorf("notifier %q bot_token: %w", n.ID, err)
			}
			spec.BotToken = token
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

// resolveSecret applies the standard env > file > inline precedence; exactly
// one is set after config validation, so this just reads whichever is
// non-empty. File paths are interpreted relative to dataDir when not
// absolute.
func resolveSecret(inline, envName, file, dataDir string) (string, error) {
	if inline = strings.TrimSpace(inline); inline != "" {
		return inline, nil
	}
	if envName = strings.TrimSpace(envName); envName != "" {
		v := os.Getenv(envName)
		if v == "" {
			return "", fmt.Errorf("env var %s is not set", envName)
		}
		return v, nil
	}
	if file = strings.TrimSpace(file); file != "" {
		path := file
		if !filepath.IsAbs(path) && dataDir != "" {
			path = filepath.Join(dataDir, file)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read secret file %s: %w", path, err)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return "", fmt.Errorf("secret source missing (validation should have caught this)")
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
