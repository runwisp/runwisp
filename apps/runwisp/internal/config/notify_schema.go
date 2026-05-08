// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/runwisp/runwisp/internal/notify/kinds"
)

var allowedNotifierTypes = []string{"slack", "telegram"}

const (
	inappNotifierID       = "inapp"
	notifyTokenSeparator  = ":"
	notifyTokenSeparatorB = ':'
)

// parseNotifyToken splits a notify destination token into its parent notifier
// id and an optional inline target override. "slack-ops:#alerts" returns
// ("slack-ops", "#alerts", true); "slack-ops" or "inapp" returns
// ("slack-ops", "", false). The "inapp" literal is never treated as having an
// override even if a colon appears, because the in-app channel has no target
// to override.
func parseNotifyToken(s string) (parentID, override string, hasOverride bool) {
	s = strings.TrimSpace(s)
	idx := strings.IndexByte(s, notifyTokenSeparatorB)
	if idx < 0 {
		return s, "", false
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:]), true
}

// toNotifyConfig translates the raw [[notifier]], [[notification_route]],
// [notify], and per-task notify_on_failure/notify_on_success blocks into a
// resolved-but-not-secret-substituted NotifyConfig.
func (t *tomlConfig) toNotifyConfig(taskNames []string, taskWires map[string]*taskWire, serviceNames []string, serviceWires map[string]*serviceWire) (NotifyConfig, error) {
	out := NotifyConfig{
		DefaultNotifiers: resolveDefaultNotifiers(t.Notify.DefaultNotifiers),
		QueueSize:        t.Notify.QueueSize,
		HistoryKeep:      t.Notify.HistoryKeep,
		OccurrenceRing:   t.Notify.OccurrenceRing,
		CoalesceOutbound: t.Notify.CoalesceOutbound == nil || *t.Notify.CoalesceOutbound,
	}

	if t.Notify.DefaultTimeout != "" {
		d, err := parseScopedDuration("notify.default_timeout", t.Notify.DefaultTimeout)
		if err != nil {
			return NotifyConfig{}, err
		}
		out.DefaultTimeout = d
	}
	if t.Notify.HistoryKeepFor != "" {
		d, err := parseScopedKeepFor("notify.history_keep_for", t.Notify.HistoryKeepFor)
		if err != nil {
			return NotifyConfig{}, err
		}
		out.HistoryKeepFor = d
	}
	if t.Notify.CoalesceWindow != "" {
		d, err := parseScopedDuration("notify.coalesce_window", t.Notify.CoalesceWindow)
		if err != nil {
			return NotifyConfig{}, err
		}
		out.CoalesceWindow = d
	}

	for i, n := range t.Notifiers {
		spec := NotifierSpec{
			ID:             strings.TrimSpace(n.ID),
			Type:           strings.TrimSpace(n.Type),
			WebhookURL:     n.WebhookURL,
			WebhookURLEnv:  n.WebhookURLEnv,
			WebhookURLFile: n.WebhookURLFile,
			SlackChannel:   n.Channel,
			BotToken:       n.BotToken,
			BotTokenEnv:    n.BotTokenEnv,
			BotTokenFile:   n.BotTokenFile,
			ChatID:         n.ChatID,
			ParseMode:      n.ParseMode,
			TemplatePath:   n.TemplatePath,
		}
		if spec.ID == "" {
			return NotifyConfig{}, fmt.Errorf("notifier #%d: id is required", i)
		}
		if spec.ID == inappNotifierID {
			return NotifyConfig{}, fmt.Errorf("notifier id %q is reserved for the in-app channel", inappNotifierID)
		}
		if strings.ContainsRune(spec.ID, notifyTokenSeparatorB) {
			return NotifyConfig{}, fmt.Errorf("notifier id %q must not contain %q (reserved for inline target overrides like %q)", spec.ID, notifyTokenSeparator, "slack:#ops")
		}
		out.Notifiers = append(out.Notifiers, spec)
	}

	for _, r := range t.Routes {
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      append([]string(nil), r.Match.Kind...),
			Severity:   strings.TrimSpace(r.Match.Severity),
			TaskGlob:   strings.TrimSpace(r.Match.Task),
			NotifierID: append([]string(nil), r.Notify...),
		})
	}

	desugarTaskNotify(taskNames, taskWires, &out)
	desugarServiceNotify(serviceNames, serviceWires, &out)
	appendDefaultRoute(&out)

	if err := expandInlineTokens(&out); err != nil {
		return NotifyConfig{}, err
	}

	return out, nil
}

// expandInlineTokens walks every route's notify list, finds tokens of the form
// "<id>:<override>", and synthesises a NotifierSpec per unique token by cloning
// the parent notifier's credentials and replacing its target (Slack channel or
// Telegram chat_id). The synthetic spec's ID is the literal token, so route
// references already match — no rewriting needed.
//
// User-declared notifier IDs cannot contain ":" (enforced earlier), so synthetic
// IDs cannot collide with user IDs. Tokens are deduped: "slack:#ops" referenced
// from three tasks produces one synthetic spec.
func expandInlineTokens(out *NotifyConfig) error {
	parentByID := make(map[string]*NotifierSpec, len(out.Notifiers))
	for i := range out.Notifiers {
		parentByID[out.Notifiers[i].ID] = &out.Notifiers[i]
	}
	seen := make(map[string]struct{})

	for _, route := range out.Routes {
		for _, tok := range route.NotifierID {
			parentID, override, hasOverride := parseNotifyToken(tok)
			if !hasOverride {
				continue
			}
			if _, done := seen[tok]; done {
				continue
			}
			seen[tok] = struct{}{}

			if parentID == "" {
				return fmt.Errorf("notify token %q: parent notifier id is empty", tok)
			}
			if override == "" {
				return fmt.Errorf("notify token %q: target override is empty after %q", tok, notifyTokenSeparator)
			}
			if parentID == inappNotifierID {
				return fmt.Errorf("notify token %q: %q has no target to override", tok, inappNotifierID)
			}
			parent, ok := parentByID[parentID]
			if !ok {
				return fmt.Errorf("notify token %q: unknown parent notifier id %q", tok, parentID)
			}
			spec, err := cloneNotifierWithOverride(*parent, tok, override)
			if err != nil {
				return err
			}
			out.Notifiers = append(out.Notifiers, spec)
		}
	}
	return nil
}

// cloneNotifierWithOverride copies a parent NotifierSpec, assigns a synthetic
// ID, and replaces the type-specific target (Slack channel or Telegram
// chat_id) with the override. Returns an error for types that have no
// overridable target.
func cloneNotifierWithOverride(parent NotifierSpec, syntheticID, override string) (NotifierSpec, error) {
	spec := parent
	spec.ID = syntheticID
	switch parent.Type {
	case "slack":
		if override[0] != '#' && override[0] != '@' {
			return NotifierSpec{}, fmt.Errorf("notify token %q: slack channel override must start with # or @ (got %q)", syntheticID, override)
		}
		spec.SlackChannel = override
	case "telegram":
		spec.ChatID = override
	default:
		return NotifierSpec{}, fmt.Errorf("notify token %q: notifier type %q does not support inline target overrides", syntheticID, parent.Type)
	}
	return spec, nil
}

// resolveDefaultNotifiers applies the documented precedence:
//   - omitted: ["inapp"] (the zero-config safety net)
//   - explicit []: no defaults — operator opted out of any built-in route
//   - explicit list: that list verbatim, deduped of empties
//
// The returned slice is always non-nil so downstream code can treat it
// uniformly; an empty slice means "no defaults", not "use built-in".
func resolveDefaultNotifiers(raw *[]string) []string {
	if raw == nil {
		return []string{inappNotifierID}
	}
	out := make([]string, 0, len(*raw))
	for _, id := range *raw {
		id = strings.TrimSpace(id)
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// appendDefaultRoute installs the zero-config safety net: every failed,
// timed-out, or crashed run fans out to every channel in default_notifiers.
// With the default ["inapp"], the bell in the Web UI and the footer in the
// TUI always light up. With ["slack-ops"], every failure pages Slack. With
// [], no synthetic route is added at all. The router deduplicates channel
// IDs across matching rules, so this is harmless when the same task also
// has explicit notify_on_failure sugar.
func appendDefaultRoute(out *NotifyConfig) {
	if len(out.DefaultNotifiers) == 0 {
		return
	}
	out.Routes = append(out.Routes, NotificationRoute{
		Kinds:      []string{"run.failed", "run.timeout", "run.crashed"},
		NotifierID: append([]string(nil), out.DefaultNotifiers...),
	})
}

// desugarTaskNotify converts per-task notify_on_failure / notify_on_success
// shorthands into synthetic routes appended to NotifyConfig.Routes.
// DefaultNotifiers (typically ["inapp"]) are appended automatically so a
// per-task slack route still lights up the in-app bell unless the operator
// explicitly cleared the default.
func desugarTaskNotify(taskNames []string, taskWires map[string]*taskWire, out *NotifyConfig) {
	for _, name := range taskNames {
		w := taskWires[name]
		if w == nil {
			continue
		}
		appendSynthRoutes(out, name, w.NotifyOnFailure, w.NotifyOnSuccess)
	}
}

func desugarServiceNotify(names []string, wires map[string]*serviceWire, out *NotifyConfig) {
	for _, name := range names {
		w := wires[name]
		if w == nil {
			continue
		}
		appendSynthRoutes(out, name, w.NotifyOnFailure, w.NotifyOnSuccess)
	}
}

func appendSynthRoutes(out *NotifyConfig, taskName string, onFailure, onSuccess []string) {
	if len(onFailure) > 0 {
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.failed", "run.timeout", "run.crashed"},
			TaskGlob:   taskName,
			NotifierID: mergeWithDefaults(onFailure, out.DefaultNotifiers),
		})
	}
	if len(onSuccess) > 0 {
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.succeeded"},
			TaskGlob:   taskName,
			NotifierID: mergeWithDefaults(onSuccess, out.DefaultNotifiers),
		})
	}
}

func mergeWithDefaults(explicit, defaults []string) []string {
	ids := append([]string(nil), explicit...)
	for _, d := range defaults {
		if !slices.Contains(ids, d) {
			ids = append(ids, d)
		}
	}
	return ids
}

// validateNotify enforces structural rules: unique IDs, correct types,
// exactly-one secret source per secret-bearing field, route IDs reference a
// known notifier (or "inapp"), kinds and severity are recognized, glob is
// well-formed.
func validateNotify(cfg *NotifyConfig) error {
	if cfg == nil {
		return nil
	}
	seenID := make(map[string]struct{}, len(cfg.Notifiers))
	for i := range cfg.Notifiers {
		spec := &cfg.Notifiers[i]
		if _, dup := seenID[spec.ID]; dup {
			return fmt.Errorf("duplicate notifier id %q", spec.ID)
		}
		seenID[spec.ID] = struct{}{}

		if err := requireOneOf(fmt.Sprintf("notifier %q type", spec.ID),
			spec.Type, allowedNotifierTypes, false); err != nil {
			return err
		}

		switch spec.Type {
		case "slack":
			if err := requireOneSecretSource(spec.ID, "webhook_url",
				spec.WebhookURL, spec.WebhookURLEnv, spec.WebhookURLFile); err != nil {
				return err
			}
			if ch := strings.TrimSpace(spec.SlackChannel); ch != "" && ch[0] != '#' && ch[0] != '@' {
				return fmt.Errorf("notifier %q: channel must start with # or @ (got %q)", spec.ID, ch)
			}
		case "telegram":
			if err := requireOneSecretSource(spec.ID, "bot_token",
				spec.BotToken, spec.BotTokenEnv, spec.BotTokenFile); err != nil {
				return err
			}
			if strings.TrimSpace(spec.ChatID) == "" {
				return fmt.Errorf("notifier %q: chat_id is required for type=telegram", spec.ID)
			}
			if strings.TrimSpace(spec.ParseMode) == "MarkdownV2" && strings.TrimSpace(spec.TemplatePath) == "" {
				return fmt.Errorf("notifier %q: parse_mode=MarkdownV2 requires template_path (the embedded default uses HTML)", spec.ID)
			}
		}
	}

	known := make(map[string]struct{}, len(seenID)+1)
	for id := range seenID {
		known[id] = struct{}{}
	}
	known[inappNotifierID] = struct{}{}

	for _, id := range cfg.DefaultNotifiers {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("notify.default_notifiers: unknown notifier id %q (declare a [[notifier]] with this id, or use \"inapp\")", id)
		}
	}

	for i, r := range cfg.Routes {
		scope := fmt.Sprintf("notification_route #%d", i)
		for _, k := range r.Kinds {
			if err := requireOneOf(scope+" match.kind", k, kinds.AllKindStrings, false); err != nil {
				return err
			}
		}
		if err := requireOneOf(scope+" match.severity", r.Severity, kinds.AllSeverityStrings, true); err != nil {
			return err
		}
		if r.TaskGlob != "" {
			if _, err := path.Match(r.TaskGlob, ""); err != nil {
				return fmt.Errorf("%s: invalid task glob %q: %w", scope, r.TaskGlob, err)
			}
		}
		if len(r.NotifierID) == 0 {
			return fmt.Errorf("%s: notify list is empty", scope)
		}
		for _, id := range r.NotifierID {
			if _, ok := known[id]; !ok {
				return fmt.Errorf("%s: unknown notifier id %q", scope, id)
			}
		}
	}
	return nil
}

// requireOneSecretSource ensures exactly one of {inline, env, file} is set for
// a secret-bearing field. Mirrors the shape of requireOneOf.
func requireOneSecretSource(id, field, inline, env, file string) error {
	count := 0
	if strings.TrimSpace(inline) != "" {
		count++
	}
	if strings.TrimSpace(env) != "" {
		count++
	}
	if strings.TrimSpace(file) != "" {
		count++
	}
	switch count {
	case 0:
		return fmt.Errorf("notifier %q: %s requires one of %s, %s_env, %s_file", id, field, field, field, field)
	case 1:
		return nil
	default:
		return fmt.Errorf("notifier %q: only one of %s, %s_env, %s_file may be set", id, field, field, field)
	}
}
