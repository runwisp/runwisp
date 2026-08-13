// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/notify/kinds"
)

var allowedNotifierTypes = []string{"slack", "discord", "telegram", "smtp", "sendmail", "webhook"}

// allowedSMTPTLSModes enumerates the values accepted for [[notifier]].tls. An
// empty string falls back to a port-derived default (465 → implicit; everything
// else → starttls).
var allowedSMTPTLSModes = []string{"starttls", "implicit", "off"}

const (
	inappNotifierID      = "inapp"
	notifyTokenSeparator = ":"
	// defaultKeepNotifications is the in-app notification cap applied when
	// notify.keep_notifications is omitted from TOML.
	defaultKeepNotifications = 1024
	// defaultKeepFor is the age limit applied when
	// notify.keep_for is omitted from TOML.
	defaultKeepFor = 90 * 24 * time.Hour
	// defaultOccurrenceRing is the coalescing ring size applied when
	// notify.occurrence_ring is omitted. It drives both the in-app occurrence
	// ring and the outbound "check-in every N events" cadence, so it must be
	// resolved here rather than left 0 (outbound treats 0 as "never check in").
	defaultOccurrenceRing = 10
)

// parseNotifyToken splits a notify destination token into its parent notifier
// id and an optional inline target override. "slack-ops:#alerts" returns
// ("slack-ops", "#alerts", true); "slack-ops" or "inapp" returns
// ("slack-ops", "", false). The "inapp" literal is never treated as having an
// override even if a colon appears, because the in-app channel has no target
// to override.
func parseNotifyToken(s string) (parentID, override string, hasOverride bool) {
	s = strings.TrimSpace(s)
	before, after, found := strings.Cut(s, notifyTokenSeparator)
	if !found {
		return s, "", false
	}
	return strings.TrimSpace(before), strings.TrimSpace(after), true
}

// toNotifyConfig translates the raw [[notifier]], [[notification_route]],
// [notify], and per-task notify_on_failure/notify_on_success blocks into a
// resolved-but-not-secret-substituted NotifyConfig.
func (t *tomlConfig) toNotifyConfig(taskNames []string, taskWires map[string]*taskWire, serviceNames []string, serviceWires map[string]*serviceWire) (NotifyConfig, error) {
	out := NotifyConfig{
		GlobalNotifiers:   resolveGlobalNotifiers(t.Notify.GlobalNotifiers),
		KeepNotifications: t.Notify.KeepNotifications,
		OccurrenceRing:    t.Notify.OccurrenceRing,
		CoalesceOutbound:  t.Notify.CoalesceOutbound == nil || *t.Notify.CoalesceOutbound,
	}
	if out.KeepNotifications == 0 {
		out.KeepNotifications = defaultKeepNotifications
	}
	if out.OccurrenceRing == 0 {
		out.OccurrenceRing = defaultOccurrenceRing
	}

	if err := t.applyNotifyDurations(&out); err != nil {
		return NotifyConfig{}, err
	}

	if err := buildNotifierSpecs(t.Notifiers, &out); err != nil {
		return NotifyConfig{}, err
	}

	for _, r := range t.Routes {
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      append([]string(nil), r.Match.Kinds...),
			Severity:   strings.TrimSpace(r.Match.Severity),
			TaskGlob:   strings.TrimSpace(r.Match.Task),
			NotifierID: append([]string(nil), r.Notify...),
		})
	}

	desugarTaskNotify(taskNames, taskWires, &out)
	desugarServiceNotify(serviceNames, serviceWires, &out)
	appendCatchAllRoute(&out)

	if err := expandInlineTokens(&out); err != nil {
		return NotifyConfig{}, err
	}

	return out, nil
}

// applyNotifyDurations parses the [notify] duration knobs and stamps them onto
// out, applying the documented default when keep_for is omitted.
func (t *tomlConfig) applyNotifyDurations(out *NotifyConfig) error {
	if t.Notify.DefaultTimeout != "" {
		d, err := parseDuration(t.Notify.DefaultTimeout)
		if err != nil {
			return fmt.Errorf("invalid notify.default_timeout: %w", err)
		}
		out.DefaultTimeout = d
	}
	if t.Notify.KeepFor != "" {
		d, err := parseKeepFor(t.Notify.KeepFor)
		if err != nil {
			return fmt.Errorf("invalid notify.keep_for: %w", err)
		}
		out.KeepFor = d
	}
	if out.KeepFor == 0 {
		out.KeepFor = defaultKeepFor
	}
	if t.Notify.CoalesceWindow != "" {
		d, err := parseDuration(t.Notify.CoalesceWindow)
		if err != nil {
			return fmt.Errorf("invalid notify.coalesce_window: %w", err)
		}
		out.CoalesceWindow = d
	}
	return nil
}

// buildNotifierSpecs translates the raw [[notifier]] entries into NotifierSpec
// rows on out. ID-shape rules (non-empty, not the reserved "inapp", no inline
// override separator) are enforced here so the rest of the pipeline can trust
// the result.
func buildNotifierSpecs(notifiers []notifierWire, out *NotifyConfig) error {
	for i, n := range notifiers {
		spec := NotifierSpec{
			ID:           strings.TrimSpace(n.ID),
			Type:         strings.TrimSpace(n.Type),
			WebhookURL:   n.WebhookURL,
			SlackChannel: n.Channel,
			BotToken:     n.BotToken,
			ChatID:       n.ChatID,
			ParseMode:    n.ParseMode,
			TemplatePath: n.TemplatePath,
			URL:          n.URL,
			Headers:      n.Headers,

			Host:          n.Host,
			Port:          n.Port,
			TLSMode:       n.TLS,
			TLSSkipVerify: n.TLSSkipVerify,
			Username:      n.Username,
			Password:      n.Password,
			From:          n.From,
			ReplyTo:       n.ReplyTo,
			Recipients:    append([]string(nil), n.To...),
			CC:            append([]string(nil), n.CC...),
			BCC:           append([]string(nil), n.BCC...),

			SendmailPath: n.SendmailPath,
		}
		if spec.ID == "" {
			return fmt.Errorf("notifier #%d: id is required", i)
		}
		if spec.ID == inappNotifierID {
			return fmt.Errorf("notifier id %q is reserved for the in-app channel", inappNotifierID)
		}
		if strings.Contains(spec.ID, notifyTokenSeparator) {
			return fmt.Errorf("notifier id %q must not contain %q (reserved for inline target overrides like %q)", spec.ID, notifyTokenSeparator, "slack:#ops")
		}
		out.Notifiers = append(out.Notifiers, spec)
	}
	return nil
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
	return expandInlineTokensFrom(out, 0)
}

// expandInlineTokensFrom resolves inline "<id>:<override>" tokens for the
// routes at index >= fromRoute, appending one synthetic NotifierSpec per unique
// token. Seeding the seen-set from the existing notifier IDs (which can never
// contain ":", enforced in buildNotifierSpecs) makes this safe to call a second
// time after new routes are appended — compose per-service notify sugar rides
// this path — without re-materialising tokens an earlier pass already resolved.
func expandInlineTokensFrom(out *NotifyConfig, fromRoute int) error {
	parentByID := make(map[string]*NotifierSpec, len(out.Notifiers))
	seen := make(map[string]struct{}, len(out.Notifiers))
	for i := range out.Notifiers {
		parentByID[out.Notifiers[i].ID] = &out.Notifiers[i]
		seen[out.Notifiers[i].ID] = struct{}{}
	}

	for _, route := range out.Routes[fromRoute:] {
		for _, tok := range route.NotifierID {
			spec, ok, err := resolveInlineToken(tok, parentByID, seen)
			if err != nil {
				return err
			}
			if ok {
				out.Notifiers = append(out.Notifiers, spec)
			}
		}
	}
	return nil
}

// resolveInlineToken returns a synthesised NotifierSpec for an "id:override"
// notify token. It returns (_, false, nil) when the token has no override or
// when an identical token was already resolved on a prior route.
func resolveInlineToken(tok string, parentByID map[string]*NotifierSpec, seen map[string]struct{}) (NotifierSpec, bool, error) {
	parentID, override, hasOverride := parseNotifyToken(tok)
	if !hasOverride {
		return NotifierSpec{}, false, nil
	}
	if _, done := seen[tok]; done {
		return NotifierSpec{}, false, nil
	}
	seen[tok] = struct{}{}

	if parentID == "" {
		return NotifierSpec{}, false, fmt.Errorf("notify token %q: parent notifier id is empty", tok)
	}
	if override == "" {
		return NotifierSpec{}, false, fmt.Errorf("notify token %q: target override is empty after %q", tok, notifyTokenSeparator)
	}
	if parentID == inappNotifierID {
		return NotifierSpec{}, false, fmt.Errorf("notify token %q: %q has no target to override", tok, inappNotifierID)
	}
	parent, ok := parentByID[parentID]
	if !ok {
		return NotifierSpec{}, false, fmt.Errorf("notify token %q: unknown parent notifier id %q", tok, parentID)
	}
	spec, err := cloneNotifierWithOverride(*parent, tok, override)
	if err != nil {
		return NotifierSpec{}, false, err
	}
	return spec, true, nil
}

// cloneNotifierWithOverride copies a parent NotifierSpec, assigns a synthetic
// ID, and replaces the type-specific target (Slack channel, Telegram chat_id,
// SMTP recipient) with the override. Returns an error for types that have no
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
	case "smtp", "sendmail":
		if _, err := mail.ParseAddress(override); err != nil {
			return NotifierSpec{}, fmt.Errorf("notify token %q: %s recipient override %q is not a valid email address: %w", syntheticID, parent.Type, override, err)
		}
		spec.Recipients = []string{override}
		spec.CC = nil
		spec.BCC = nil
	case "discord":
		return NotifierSpec{}, fmt.Errorf("notify token %q: discord notifiers do not support inline target overrides (a Discord webhook is bound to one channel)", syntheticID)
	case "webhook":
		return NotifierSpec{}, fmt.Errorf("notify token %q: webhook notifiers do not support inline target overrides", syntheticID)
	default:
		return NotifierSpec{}, fmt.Errorf("notify token %q: notifier type %q does not support inline target overrides", syntheticID, parent.Type)
	}
	return spec, nil
}

// resolveGlobalNotifiers applies the documented precedence:
//   - omitted: ["inapp"] (the zero-config safety net)
//   - explicit []: no global channels — operator opted out of any built-in route
//   - explicit list: that list verbatim, deduped of empties
//
// The returned slice is always non-nil so downstream code can treat it
// uniformly; an empty slice means "nothing to append", not "use built-in".
func resolveGlobalNotifiers(raw *[]string) []string {
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

// appendCatchAllRoute installs the zero-config safety net: every failed,
// timed-out, crashed, or missed run fans out to every channel in
// global_notifiers. With the default ["inapp"], the bell in the Web UI and the
// footer in the TUI always light up. With ["slack-ops"], every failure pages
// Slack. With [], no synthetic route is added at all. The router deduplicates
// channel IDs across matching rules, so this is harmless when the same task
// also has explicit notify_on_failure sugar. run.missed rides this route by
// default (decision: misses reach whoever gets failures); a task silences it
// with treat_missed_as_failure = false, applied as a mute in the notify subsystem.
func appendCatchAllRoute(out *NotifyConfig) {
	if len(out.GlobalNotifiers) == 0 {
		return
	}
	out.Routes = append(out.Routes, NotificationRoute{
		Kinds:      []string{"run.failed", "run.timeout", "run.crashed", "run.missed", "service.fatal"},
		NotifierID: append([]string(nil), out.GlobalNotifiers...),
	})
}

// desugarTaskNotify converts per-task notify_on_failure / notify_on_success
// shorthands into synthetic routes appended to NotifyConfig.Routes.
// GlobalNotifiers (typically ["inapp"]) are appended automatically so a
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
		// run.missed joins the failure kinds so notify_on_failure = ["slack"]
		// also pages Slack on a missed run — the operator opted into failure
		// alerts there. Silencing misses is a one-line treat_missed_as_failure = false,
		// not a rewrite of this list into a hand-authored route.
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.failed", "run.timeout", "run.crashed", "run.missed", "service.fatal"},
			TaskGlob:   taskName,
			NotifierID: mergeWithAppended(onFailure, out.GlobalNotifiers),
		})
	}
	if len(onSuccess) > 0 {
		out.Routes = append(out.Routes, NotificationRoute{
			Kinds:      []string{"run.succeeded"},
			TaskGlob:   taskName,
			NotifierID: mergeWithAppended(onSuccess, out.GlobalNotifiers),
		})
	}
}

func mergeWithAppended(explicit, appended []string) []string {
	ids := append([]string(nil), explicit...)
	for _, d := range appended {
		if !slices.Contains(ids, d) {
			ids = append(ids, d)
		}
	}
	return ids
}

// validateNotify enforces structural rules: unique IDs, correct types,
// required per-type fields, route IDs reference a known notifier (or
// "inapp"), kinds and severity are recognized, glob is well-formed.
func validateNotify(cfg *NotifyConfig) error {
	if cfg == nil {
		return nil
	}
	seenID, err := validateNotifierSpecs(cfg.Notifiers)
	if err != nil {
		return err
	}

	known := buildKnownNotifierSet(seenID)

	for _, id := range cfg.GlobalNotifiers {
		if _, ok := known[id]; !ok {
			return fmt.Errorf("notify.global_notifiers: unknown notifier id %q (declare a [[notifier]] with this id, or use \"inapp\")", id)
		}
	}

	for i, r := range cfg.Routes {
		if err := validateRoute(i, r, known); err != nil {
			return err
		}
	}
	return nil
}

// validateNotifierSpecs runs per-spec structural checks (unique id, correct
// type, type-specific required fields) and returns the set of declared ids
// on success.
func validateNotifierSpecs(specs []NotifierSpec) (map[string]struct{}, error) {
	seenID := make(map[string]struct{}, len(specs))
	for i := range specs {
		spec := &specs[i]
		if _, dup := seenID[spec.ID]; dup {
			return nil, fmt.Errorf("duplicate notifier id %q", spec.ID)
		}
		seenID[spec.ID] = struct{}{}

		if err := requireOneOf(fmt.Sprintf("notifier %q type", spec.ID),
			spec.Type, allowedNotifierTypes, false); err != nil {
			return nil, err
		}
		if err := validateNotifierByType(spec); err != nil {
			return nil, err
		}
	}
	return seenID, nil
}

func validateNotifierByType(spec *NotifierSpec) error {
	switch spec.Type {
	case "slack":
		return validateSlackNotifier(spec)
	case "discord":
		return validateDiscordNotifier(spec)
	case "telegram":
		return validateTelegramNotifier(spec)
	case "smtp":
		return validateSMTPNotifier(spec)
	case "sendmail":
		return validateSendmailNotifier(spec)
	case "webhook":
		return validateWebhookNotifier(spec)
	}
	return nil
}

func validateSlackNotifier(spec *NotifierSpec) error {
	if strings.TrimSpace(spec.WebhookURL) == "" {
		return fmt.Errorf("notifier %q: webhook_url is required for type=slack", spec.ID)
	}
	ch := strings.TrimSpace(spec.SlackChannel)
	if ch != "" && ch[0] != '#' && ch[0] != '@' {
		return fmt.Errorf("notifier %q: channel must start with # or @ (got %q)", spec.ID, ch)
	}
	return nil
}

func validateSMTPNotifier(spec *NotifierSpec) error {
	if strings.TrimSpace(spec.Host) == "" {
		return fmt.Errorf("notifier %q: host is required for type=smtp", spec.ID)
	}
	if spec.Port < 0 || spec.Port > 65535 {
		return fmt.Errorf("notifier %q: port %d is out of range", spec.ID, spec.Port)
	}
	if err := requireOneOf(fmt.Sprintf("notifier %q tls", spec.ID),
		spec.TLSMode, allowedSMTPTLSModes, true); err != nil {
		return err
	}
	if err := validateMailAddresses(spec, "smtp"); err != nil {
		return err
	}
	return validateSMTPAuth(spec)
}

// validateSendmailNotifier checks a local-MTA notifier. It shares the
// addressing rules with SMTP and has no relay settings of its own: the MTA
// already holds the host, the port and the credentials, which is the reason to
// use it. sendmail_path is validated for shape only — whether the binary is
// actually there is resolved at send time, since an MTA can be installed after
// the daemon boots.
func validateSendmailNotifier(spec *NotifierSpec) error {
	if p := strings.TrimSpace(spec.SendmailPath); p != "" && !path.IsAbs(p) {
		return fmt.Errorf("notifier %q: sendmail_path %q must be an absolute path", spec.ID, p)
	}
	return validateMailAddresses(spec, "sendmail")
}

// validateMailAddresses checks the From/Reply-To/To/Cc/Bcc set shared by every
// mail-shaped notifier. typeName appears in the messages so an operator is told
// which type they got the requirement from.
func validateMailAddresses(spec *NotifierSpec, typeName string) error {
	from := strings.TrimSpace(spec.From)
	if from == "" {
		return fmt.Errorf("notifier %q: from is required for type=%s", spec.ID, typeName)
	}
	if _, err := mail.ParseAddress(from); err != nil {
		return fmt.Errorf("notifier %q: from %q is not a valid email address: %w", spec.ID, from, err)
	}
	if rt := strings.TrimSpace(spec.ReplyTo); rt != "" {
		if _, err := mail.ParseAddress(rt); err != nil {
			return fmt.Errorf("notifier %q: reply_to %q is not a valid email address: %w", spec.ID, rt, err)
		}
	}
	if len(spec.Recipients) == 0 {
		return fmt.Errorf("notifier %q: to is required for type=%s (at least one recipient)", spec.ID, typeName)
	}
	groups := []struct {
		label string
		addrs []string
	}{
		{"to", spec.Recipients},
		{"cc", spec.CC},
		{"bcc", spec.BCC},
	}
	for _, g := range groups {
		if err := validateAddressList(spec.ID, g.label, g.addrs); err != nil {
			return err
		}
	}
	return nil
}

func validateAddressList(notifierID, label string, addrs []string) error {
	for _, a := range addrs {
		if _, err := mail.ParseAddress(a); err != nil {
			return fmt.Errorf("notifier %q: %s address %q is not valid: %w", notifierID, label, a, err)
		}
	}
	return nil
}

func validateSMTPAuth(spec *NotifierSpec) error {
	hasUser := strings.TrimSpace(spec.Username) != ""
	hasPassword := strings.TrimSpace(spec.Password) != ""
	if hasUser != hasPassword {
		return fmt.Errorf("notifier %q: username and password must be set together (or both omitted for auth-less relays)", spec.ID)
	}
	if hasPassword && strings.TrimSpace(spec.TLSMode) == "off" {
		return fmt.Errorf("notifier %q: tls=\"none\" is not allowed with credentials — refuse to send PLAIN auth over cleartext", spec.ID)
	}
	return nil
}

func validateTelegramNotifier(spec *NotifierSpec) error {
	if strings.TrimSpace(spec.BotToken) == "" {
		return fmt.Errorf("notifier %q: bot_token is required for type=telegram", spec.ID)
	}
	if strings.TrimSpace(spec.ChatID) == "" {
		return fmt.Errorf("notifier %q: chat_id is required for type=telegram", spec.ID)
	}
	if strings.TrimSpace(spec.ParseMode) == "MarkdownV2" && strings.TrimSpace(spec.TemplatePath) == "" {
		return fmt.Errorf("notifier %q: parse_mode=MarkdownV2 requires template_path (the embedded default uses HTML)", spec.ID)
	}
	return nil
}

func validateDiscordNotifier(spec *NotifierSpec) error {
	raw := strings.TrimSpace(spec.WebhookURL)
	if raw == "" {
		return fmt.Errorf("notifier %q: webhook_url is required for type=discord", spec.ID)
	}
	return validateHTTPURL(spec.ID, "webhook_url", raw)
}

func validateWebhookNotifier(spec *NotifierSpec) error {
	raw := strings.TrimSpace(spec.URL)
	if raw == "" {
		return fmt.Errorf("notifier %q: url is required for type=webhook", spec.ID)
	}
	if err := validateHTTPURL(spec.ID, "url", raw); err != nil {
		return err
	}
	for k := range spec.Headers {
		if strings.TrimSpace(k) == "" {
			return fmt.Errorf("notifier %q: header key must not be empty", spec.ID)
		}
	}
	return nil
}

// validateHTTPURL checks that raw parses as an absolute http(s) URL with a
// host. key names the offending TOML key in error messages.
func validateHTTPURL(notifierID, key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("notifier %q: invalid %s: %w", notifierID, key, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("notifier %q: %s must use http or https scheme (got %q)", notifierID, key, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("notifier %q: %s is missing host", notifierID, key)
	}
	return nil
}

func buildKnownNotifierSet(seenID map[string]struct{}) map[string]struct{} {
	known := make(map[string]struct{}, len(seenID)+1)
	for id := range seenID {
		known[id] = struct{}{}
	}
	known[inappNotifierID] = struct{}{}
	return known
}

func validateRoute(idx int, r NotificationRoute, known map[string]struct{}) error {
	scope := fmt.Sprintf("notification_route #%d", idx)
	for _, k := range r.Kinds {
		if err := requireOneOf(scope+" match.kinds", k, kinds.AllKindStrings, false); err != nil {
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
	return nil
}
