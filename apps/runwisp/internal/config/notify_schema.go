// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path"
	"strings"
)

// allowedNotifyKinds enumerates the public Kind values that may appear in
// [[notification_route]] match.kind. Kept in sync with notify.AllKinds; we do
// not import notify here to avoid pulling the runtime subsystem into config.
var allowedNotifyKinds = []string{
	"run.started",
	"run.succeeded",
	"run.failed",
	"run.timeout",
	"run.stopped",
	"run.crashed",
	"notify.delivery_failed",
}

var allowedNotifySeverities = []string{"info", "warn", "error"}
var allowedNotifierTypes = []string{"slack", "telegram"}

const inappNotifierID = "inapp"

// toNotifyConfig translates the raw [[notifier]], [[notification_route]],
// [notify], and per-task notify_on_failure/notify_on_success blocks into a
// resolved-but-not-secret-substituted NotifyConfig.
func (t *tomlConfig) toNotifyConfig(taskNames []string, taskWires map[string]*taskWire, serviceNames []string, serviceWires map[string]*serviceWire) (NotifyConfig, error) {
	out := NotifyConfig{
		DisableInapp:   t.Notify.DisableInapp,
		QueueSize:      t.Notify.QueueSize,
		HistoryKeep:    t.Notify.HistoryKeep,
		OccurrenceRing: t.Notify.OccurrenceRing,
	}

	if t.Notify.DefaultTimeout != "" {
		d, err := parseScopedDuration("notify.default_timeout", t.Notify.DefaultTimeout)
		if err != nil {
			return NotifyConfig{}, err
		}
		out.DefaultTimeout = d
	}
	if t.Notify.HistoryKeepFor != "" {
		d, err := parseScopedDuration("notify.history_keep_for", t.Notify.HistoryKeepFor)
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
	appendDefaultInappRoute(&out)

	return out, nil
}

// appendDefaultInappRoute installs the zero-config in-app safety net: every
// failed, timed-out, or crashed run lights up the bell in the Web UI and the
// footer in the TUI, even when the operator has not declared a single
// notifier or route. Opt out with [notify] disable_inapp = true. The router
// deduplicates channel IDs across matching rules, so this is harmless when
// the same task also has explicit notify_on_failure sugar.
func appendDefaultInappRoute(out *NotifyConfig) {
	if out.DisableInapp {
		return
	}
	out.Routes = append(out.Routes, NotificationRoute{
		Kinds:      []string{"run.failed", "run.timeout", "run.crashed"},
		NotifierID: []string{inappNotifierID},
	})
}

// desugarTaskNotify converts per-task notify_on_failure / notify_on_success
// shorthands into synthetic routes appended to NotifyConfig.Routes. The
// implicit "inapp" target is added unless DisableInapp is set.
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
		ids := append([]string(nil), onFailure...)
		if !out.DisableInapp && !containsString(ids, inappNotifierID) {
			ids = append(ids, inappNotifierID)
		}
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.failed", "run.timeout", "run.crashed"},
			TaskGlob:   taskName,
			NotifierID: ids,
		})
	}
	if len(onSuccess) > 0 {
		ids := append([]string(nil), onSuccess...)
		if !out.DisableInapp && !containsString(ids, inappNotifierID) {
			ids = append(ids, inappNotifierID)
		}
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.succeeded"},
			TaskGlob:   taskName,
			NotifierID: ids,
		})
	}
}

func containsString(xs []string, target string) bool {
	for _, x := range xs {
		if x == target {
			return true
		}
	}
	return false
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

	for i, r := range cfg.Routes {
		scope := fmt.Sprintf("notification_route #%d", i)
		for _, k := range r.Kinds {
			if err := requireOneOf(scope+" match.kind", k, allowedNotifyKinds, false); err != nil {
				return err
			}
		}
		if err := requireOneOf(scope+" match.severity", r.Severity, allowedNotifySeverities, true); err != nil {
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
