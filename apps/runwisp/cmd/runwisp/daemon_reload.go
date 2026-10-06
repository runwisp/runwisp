// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"reflect"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/update"
)

// settingsApplier is the reconciler's SettingsHook: it swaps the daemon-wide
// settings a reload changed into the running subsystems. Every key
// runtime.changedSettings reports must be pushed in commit below, or a reload
// reports it applied while the daemon keeps the old value. srv is set once the
// server exists, before any listener can deliver a reload.
type settingsApplier struct {
	svc         *daemonServices
	fingerprint string
	updates     *update.Checker
	srv         *server.Server
	// templates holds the template_path contents the live notification service
	// was built from (see notifyTemplates). Only prepare and its commit touch
	// it, and the reconciler serialises reloads.
	templates map[string]string
}

// notifyTemplates reads every notifier's template_path, so a reload notices an
// edited template even when runwisp.toml itself is unchanged. An unreadable file
// maps to "" here; building the service reports the real error.
func notifyTemplates(cfg config.NotifyConfig) map[string]string {
	out := make(map[string]string)
	for _, n := range cfg.Notifiers {
		if n.TemplatePath != "" {
			body, _ := os.ReadFile(n.TemplatePath)
			out[n.TemplatePath] = string(body)
		}
	}
	return out
}

// prepare builds everything that can fail (today only the notification
// service) without touching live state, then returns the commit that swaps the
// new settings in. Notify is rebuilt only when its inputs changed, template
// files included, so an unrelated reload leaves coalescing windows and
// in-flight retries alone.
func (a *settingsApplier) prepare(old, updated *config.Config) (func(), error) {
	templates := notifyTemplates(updated.Notify)
	rebuildNotify := !reflect.DeepEqual(old.Notify, updated.Notify) ||
		old.Daemon.ExternalURL != updated.Daemon.ExternalURL ||
		!maps.Equal(a.templates, templates)
	var nextNotify *notify.Service
	if rebuildNotify {
		var err error
		nextNotify, err = initNotify(updated, a.fingerprint, a.svc.Notify.Hub, a.svc.DB, a.svc.EventBus, slog.Default())
		if err != nil {
			return nil, fmt.Errorf("notifications: %w", err)
		}
	}

	return func() {
		if rebuildNotify {
			a.svc.Notify.swap(nextNotify)
			a.templates = templates
		}
		a.svc.Executor.SetMinFreeDisk(updated.Storage.MinFreeSpace)
		a.svc.RetentionCleaner.SetMaxTotalSize(updated.Storage.MaxSize)
		a.svc.TaskShutdownTimeout.Store(int64(updated.Daemon.ShutdownTimeout))
		a.updates.SetEnabled(updated.Daemon.CheckUpdates)
		if a.srv == nil {
			return
		}
		// Config already validated the CIDRs; an error here is a bug, not bad input.
		if err := a.srv.SetTrustedProxies(updated.Daemon.TrustedProxies); err != nil {
			slog.Error("Failed to apply reloaded trusted_proxies; keeping the previous set", "err", err)
		}
		a.srv.UpdateDaemonInfo(func(info *model.DaemonInfo) {
			info.ExternalURL = updated.Daemon.ExternalURL
			info.ResolvedTimezone = updated.Scheduler.Timezone
			info.TimezoneSource = updated.Scheduler.Source
			info.CheckUpdates = updated.Daemon.CheckUpdates
		})
	}, nil
}
