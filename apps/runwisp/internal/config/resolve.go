// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/secretref"
)

// resolveTypedFields interpolates ${...} placeholders in the type-constrained
// and path fields of the decoded wire config, in place, before those strings
// are parsed into durations / sizes / enums / paths. Resolving first means a
// value like timeout = "${T}" parses and validates against the resolved value,
// with no ${-detection branch needed downstream.
//
// Free-form fields are deliberately NOT passed through here: run, inline env
// values, description, and group keep their raw placeholder so the resolved
// value never lands in config.Config — and therefore never reaches the REST
// API, Web UI, or captured logs (those resolve late, at the consuming seam).
// Notifier secrets (webhook_url, bot_token, password) are likewise left raw and
// resolved at the notify seam in internal/notify/configload.
func resolveTypedFields(raw *tomlConfig, dataDir string) error {
	r := typedResolver{dataDir: dataDir}

	for _, w := range raw.Tasks {
		r.ptr(&w.Cron)
		r.ptr(&w.Timezone)
		r.ptr(&w.Timeout)
		r.ptr(&w.GracefulStop)
		r.ptr(&w.RetryDelay)
		r.ptr(&w.RetryBackoff)
		r.ptr(&w.LogMaxSize)
		r.ptr(&w.LogOnFull)
		r.ptr(&w.KeepFor)
		w.CatchUp = model.MissedRunPolicy(r.str(string(w.CatchUp)))
		w.Restart = model.RestartPolicy(r.str(string(w.Restart)))
		w.OnOverlap = model.ConcurrencyPolicy(r.str(string(w.OnOverlap)))
		r.ptr(&w.ComposeFile)
		r.ptr(&w.ComposeService)
		r.ptr(&w.EnvFile)
	}

	for _, w := range raw.Services {
		r.ptr(&w.Timeout)
		r.ptr(&w.GracefulStop)
		w.OnOverlap = model.ConcurrencyPolicy(r.str(string(w.OnOverlap)))
		r.ptr(&w.RestartDelay)
		r.ptr(&w.RestartBackoff)
		r.ptr(&w.BackoffResetAfter)
		r.ptr(&w.LogMaxSize)
		r.ptr(&w.LogOnFull)
		r.ptr(&w.KeepFor)
		r.ptr(&w.ComposeFile)
		r.ptr(&w.ComposeService)
		r.ptr(&w.EnvFile)
	}

	r.ptr(&raw.Defaults.Timeout)
	r.ptr(&raw.Defaults.LogMaxSize)
	r.ptr(&raw.Defaults.LogOnFull)
	r.ptr(&raw.Defaults.KeepFor)
	r.ptr(&raw.Defaults.BackoffResetAfter)
	r.ptr(&raw.Defaults.EnvFile)

	// reveal_vars stays raw — it lists env var names, not placeholders.
	r.ptr(&raw.Daemon.ShutdownTimeout)
	r.ptr(&raw.Daemon.ExternalURL)
	r.ptr(&raw.Daemon.MetricsListen)

	r.ptr(&raw.Storage.MaxSize)
	r.ptr(&raw.Storage.MinFreeSpace)

	r.ptr(&raw.Scheduler.Timezone)

	r.ptr(&raw.Notify.DefaultTimeout)
	r.ptr(&raw.Notify.HistoryKeepFor)
	r.ptr(&raw.Notify.CoalesceWindow)

	for i := range raw.Notifiers {
		n := &raw.Notifiers[i]
		r.ptr(&n.Channel)
		r.ptr(&n.ChatID)
		r.ptr(&n.ParseMode)
		r.ptr(&n.TemplatePath)
		r.ptr(&n.Host)
		r.ptr(&n.TLS)
		r.ptr(&n.Username)
		r.ptr(&n.From)
		r.ptr(&n.ReplyTo)
		r.list(n.To)
		r.list(n.CC)
		r.list(n.BCC)
	}

	return r.err
}

// typedResolver resolves placeholders in place, latching the first error so a
// missing ${VAR} (with no :-default) or an unreadable ${file:...} fails the
// whole load loudly.
type typedResolver struct {
	dataDir string
	err     error
}

func (r *typedResolver) str(in string) string {
	if r.err != nil || !secretref.Contains(in) {
		return in
	}
	v, _, err := secretref.Resolve(in, r.dataDir)
	if err != nil {
		r.err = err
		return in
	}
	return v
}

func (r *typedResolver) ptr(p *string) { *p = r.str(*p) }

func (r *typedResolver) list(items []string) {
	for i := range items {
		items[i] = r.str(items[i])
	}
}

// CollectHiddenSecrets resolves every free-form, runtime-consumed value (inline
// env values, run scripts, env_file-derived values) and every notifier secret,
// returning the individual resolved secret substrings that must stay hidden.
// Each ${...} placeholder contributes its own resolved value (never the whole
// interpolated string) so the redaction backstop masks the secret without
// clobbering surrounding non-secret text. A placeholder's value is hidden
// unless it is a revealable ${VAR} listed in reveal; ${file:...} references and
// notifier secrets are always hidden. The returned values seed the redaction
// backstop at boot.
//
// It errors when any referenced ${VAR} is unset (and has no :-default) or any
// ${file:...} is unreadable — this is how both `runwisp validate` and daemon
// boot fail loudly on a missing secret rather than starting half-wired. Callers
// that only need the presence check (validate) pass a nil reveal set and
// discard the returned slice.
func CollectHiddenSecrets(cfg *Config, dataDir string, reveal map[string]bool) ([]string, error) {
	c := secretCollector{dataDir: dataDir, reveal: reveal}

	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		if err := c.inline(fmt.Sprintf("run for task %s", t.Name), t.Run); err != nil {
			return nil, err
		}
		for k, v := range t.Env {
			if err := c.inline(fmt.Sprintf("env %q for task %s", k, t.Name), v); err != nil {
				return nil, err
			}
		}
		// env_file values are literal secrets the operator pointed us at; they
		// carry no placeholders and are never shown — always hidden.
		for _, v := range t.SecretEnv {
			c.hidden = append(c.hidden, v)
		}
	}
	for k, v := range cfg.Defaults.Env {
		if err := c.inline(fmt.Sprintf("defaults.env %q", k), v); err != nil {
			return nil, err
		}
	}
	for _, v := range cfg.Defaults.SecretEnv {
		c.hidden = append(c.hidden, v)
	}

	for i := range cfg.Notify.Notifiers {
		n := &cfg.Notify.Notifiers[i]
		fields := []struct{ scope, raw string }{
			{fmt.Sprintf("notifier %q webhook_url", n.ID), n.WebhookURL},
			{fmt.Sprintf("notifier %q bot_token", n.ID), n.BotToken},
			{fmt.Sprintf("notifier %q password", n.ID), n.Password},
		}
		for _, f := range fields {
			if err := c.alwaysHidden(f.scope, f.raw); err != nil {
				return nil, err
			}
		}
	}

	return c.hidden, nil
}

// secretCollector accumulates the resolved secret substrings that must be
// redacted, latching the resolution it performs to a single dataDir + reveal
// set. inline respects reveal; alwaysHidden ignores it (notifier secrets).
type secretCollector struct {
	dataDir string
	reveal  map[string]bool
	hidden  []string
}

func (c *secretCollector) inline(scope, raw string) error {
	return c.collect(scope, raw, false)
}

func (c *secretCollector) alwaysHidden(scope, raw string) error {
	return c.collect(scope, raw, true)
}

// collect resolves raw and appends each placeholder's value to hidden when it
// must stay hidden. force overrides reveal for whole-secret fields.
func (c *secretCollector) collect(scope, raw string, force bool) error {
	if !secretref.Contains(raw) {
		return nil // a plain literal is visible in the TOML; nothing to hide
	}
	_, refs, err := secretref.Resolve(raw, c.dataDir)
	if err != nil {
		return fmt.Errorf("%s: %w", scope, err)
	}
	for _, ref := range refs {
		if force || refHidden(ref, c.reveal) {
			c.hidden = append(c.hidden, ref.Value)
		}
	}
	return nil
}

// refHidden reports whether a single placeholder must stay hidden: a file
// reference always is, and an env reference is hidden unless its name is in
// reveal (forgetting to reveal a name always hides — it never leaks).
func refHidden(ref secretref.Ref, reveal map[string]bool) bool {
	return ref.FromFile || !reveal[ref.Name]
}
