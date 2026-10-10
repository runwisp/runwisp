// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"reflect"
	"slices"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/runwisp/runwisp/apps/runwisp/internal/update"
)

// settingsApplier is the reconciler's SettingsHook: it swaps the daemon-wide
// settings a reload changed into the running subsystems. srv is set once the
// server exists, before any listener can deliver a reload.
type settingsApplier struct {
	svc         *daemonServices
	fingerprint string
	updates     *update.Checker
	srv         *server.Server
	// readFile reads template_path files; nil means os.ReadFile.
	readFile func(string) ([]byte, error)
}

// readNotifyTemplates reads every notifier's template_path, keyed by path. The
// notification service is built from exactly these bodies, so a reload that
// compares them sees the same bytes it would build from. An unreadable file is
// left out of the map and reported in the error, so a file that stays
// unreadable doesn't count as a change.
func readNotifyTemplates(cfg config.NotifyConfig, read func(string) ([]byte, error)) (map[string]string, error) {
	out := make(map[string]string)
	var errs []error
	for _, n := range cfg.Notifiers {
		if n.TemplatePath == "" {
			continue
		}
		body, err := read(n.TemplatePath)
		if err != nil {
			errs = append(errs, fmt.Errorf("read template %s: %w", n.TemplatePath, err))
			continue
		}
		out[n.TemplatePath] = string(body)
	}
	return out, errors.Join(errs...)
}

// prepare builds everything that can fail (today only the notification
// service) without touching live state. It returns the TOML keys this reload
// changes and the commit that applies exactly those, so a key is never
// reported without being applied. Notify is rebuilt only when its inputs
// changed, template files included, so an unrelated reload leaves coalescing
// windows and in-flight retries alone.
func (a *settingsApplier) prepare(old, updated *config.Config) ([]string, func(), error) {
	var keys []string
	var steps []func()
	set := func(changed bool, key string, apply func()) {
		if changed {
			keys = append(keys, key)
			steps = append(steps, apply)
		}
	}

	notifyChanged, swapNotify, err := a.prepareNotify(old, updated)
	if err != nil {
		return nil, nil, err
	}
	if swapNotify != nil {
		steps = append(steps, swapNotify)
	}

	// Applied by the notify rebuild and the daemon info refresh below.
	set(old.Daemon.Name != updated.Daemon.Name, "daemon.name", func() {})
	set(old.Daemon.ExternalURL != updated.Daemon.ExternalURL, "daemon.external_url", func() {})
	set(old.Daemon.CheckUpdates != updated.Daemon.CheckUpdates, "daemon.check_updates", func() {
		a.updates.SetEnabled(updated.Daemon.CheckUpdates)
	})
	set(old.Daemon.ShutdownTimeout != updated.Daemon.ShutdownTimeout, "daemon.shutdown_timeout", func() {
		a.svc.TaskShutdownTimeout.Store(int64(updated.Daemon.ShutdownTimeout))
	})
	set(!slices.Equal(old.Daemon.TrustedProxies, updated.Daemon.TrustedProxies), "daemon.trusted_proxies", func() {
		if a.srv == nil {
			return
		}
		// Config already validated the CIDRs; an error here is a bug, not bad input.
		if err := a.srv.SetTrustedProxies(updated.Daemon.TrustedProxies); err != nil {
			slog.Error("Failed to apply reloaded trusted_proxies; keeping the previous set", "err", err)
		}
	})
	set(old.Storage.MaxSize != updated.Storage.MaxSize, "storage.max_size", func() {
		a.svc.RetentionCleaner.SetMaxTotalSize(updated.Storage.MaxSize)
	})
	set(old.Storage.MinFreeSpace != updated.Storage.MinFreeSpace, "storage.min_free_space", func() {
		a.svc.Executor.SetMinFreeDisk(updated.Storage.MinFreeSpace)
	})
	if notifyChanged {
		keys = append(keys, "notifications")
	}

	return keys, func() {
		for _, step := range steps {
			step()
		}
		// /api/daemon mirrors the applied config, timezone included (the
		// reconciler re-bases the schedules). Idempotent, so it isn't keyed.
		if a.srv != nil {
			a.srv.UpdateDaemonInfo(func(info *model.DaemonInfo) {
				info.Name = updated.Daemon.Name
				info.ExternalURL = updated.Daemon.ExternalURL
				info.ResolvedTimezone = updated.Scheduler.Timezone
				info.TimezoneSource = updated.Scheduler.Source
				info.CheckUpdates = updated.Daemon.CheckUpdates
			})
		}
	}, nil
}

// prepareNotify builds the replacement notification service when its inputs
// changed: [notify], notifiers and routes, the template_path bodies, or the
// external URL baked into every notifier's links. changed reports the first
// three (the "notifications" key); swap is nil when nothing needs rebuilding.
func (a *settingsApplier) prepareNotify(old, updated *config.Config) (changed bool, swap func(), err error) {
	read := a.readFile
	if read == nil {
		read = os.ReadFile
	}
	templates, readErr := readNotifyTemplates(updated.Notify, read)
	changed = !reflect.DeepEqual(old.Notify, updated.Notify) || !maps.Equal(a.svc.Notify.templates, templates)
	if !changed && old.Daemon.ExternalURL == updated.Daemon.ExternalURL && old.Daemon.Name == updated.Daemon.Name {
		return false, nil, nil
	}
	if readErr != nil {
		return false, nil, fmt.Errorf("notifications: %w", readErr)
	}
	next, err := initNotify(updated, templates, a.fingerprint, a.svc.Notify.Hub, a.svc.DB, a.svc.EventBus, slog.Default())
	if err != nil {
		return false, nil, fmt.Errorf("notifications: %w", err)
	}
	return changed, func() {
		a.svc.Notify.swap(next)
		a.svc.Notify.templates = templates
	}, nil
}
