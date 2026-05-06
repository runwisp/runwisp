// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/notify/configload"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/runwisp/runwisp/internal/storage"
)

// notifyBundle owns the runtime objects the rest of the daemon needs after
// notify is wired: the Service (for lifecycle) and the inapp Hub (for the SSE
// handler in internal/server). Hub may be nil when DisableInapp is set.
type notifyBundle struct {
	Service *notify.Service
	Hub     *inapp.Hub
}

// initNotify constructs the notification subsystem from the daemon config.
// Returns a zero bundle (Service nil) when there are no notifiers and no
// routes — the daemon then runs without notifications wired at all.
func initNotify(
	cfg *daemonConfig,
	db storage.Database,
	bus events.EventBus,
	logger *slog.Logger,
) (notifyBundle, error) {
	notifyCfg := cfg.Config.Notify
	if len(notifyCfg.Notifiers) == 0 && len(notifyCfg.Routes) == 0 && notifyCfg.DisableInapp {
		return notifyBundle{}, nil
	}

	resolved, err := configload.Resolve(notifyCfg, flags.DataDir)
	if err != nil {
		return notifyBundle{}, fmt.Errorf("resolve notify config: %w", err)
	}

	// No notifiers and no rules: nothing to do. Skip every goroutine and the
	// bus subscription. The server's notification routes still respond from
	// the persistent repo; the SSE stream falls back to ping-only.
	if len(resolved.Notifiers) == 0 && len(resolved.Rules) == 0 {
		return notifyBundle{}, nil
	}

	if override := backoffOverride(notifyCfg.DefaultTimeout, logger); override != nil {
		for i := range resolved.Notifiers {
			resolved.Notifiers[i].Transport = override()
		}
	}

	channels := make([]notify.Channel, 0, len(resolved.Notifiers)+1)

	var hub *inapp.Hub
	var inappCh *inapp.Channel
	if !notifyCfg.DisableInapp {
		hub = inapp.NewHub(32, 50)

		coalescerCfg := inapp.CoalescerConfig{
			Window:      notifyCfg.CoalesceWindow,
			OccurrenceN: notifyCfg.OccurrenceRing,
		}
		coalescer := inapp.NewCoalescer(db, hub, notify.RealClock(), coalescerCfg, logger)

		inappRenderer, err := buildInappRenderer()
		if err != nil {
			return notifyBundle{}, err
		}

		inappCh = inapp.New("inapp", inappRenderer, coalescer)
		channels = append(channels, inappCh)
	}

	for _, spec := range resolved.Notifiers {
		ch, err := channel.Build(spec)
		if err != nil {
			return notifyBundle{}, fmt.Errorf("build notifier %q: %w", spec.ID, err)
		}
		channels = append(channels, ch)
	}

	var failureSink notify.FailureSink
	if inappCh != nil {
		failureSink = inappCh
	}

	retentionFn := buildRetentionFn(db, notifyCfg, logger)

	svc := notify.New(notify.Config{
		Bus:            bus,
		Channels:       channels,
		Rules:          resolved.Rules,
		FailureSink:    failureSink,
		IngressSize:    notifyCfg.QueueSize,
		Logger:         logger,
		RetentionEvery: 5 * time.Minute,
		RetentionFn:    retentionFn,
	})

	return notifyBundle{Service: svc, Hub: hub}, nil
}

// backoffOverride returns a transport-builder that shrinks the outbound retry
// budget when default_timeout is set in TOML. A separate transport is
// constructed per-channel so per-channel Body429Fn customisation still applies.
func backoffOverride(d time.Duration, logger *slog.Logger) func() *notify.HTTPProvider {
	if d <= 0 {
		return nil
	}
	logger.Info("notify backoff overridden via default_timeout", "max_elapsed", d)
	return func() *notify.HTTPProvider {
		t := notify.NewHTTPProvider()
		bo := t.Backoff
		bo.MaxElapsedTime = d
		if bo.InitialInterval > d {
			bo.InitialInterval = d / 4
		}
		if bo.MaxInterval > d {
			bo.MaxInterval = d
		}
		t.Backoff = bo
		return t
	}
}

func buildInappRenderer() (render.Renderer, error) {
	body, err := render.LoadDefaultTemplate("inapp")
	if err != nil {
		return nil, fmt.Errorf("load inapp template: %w", err)
	}
	return render.NewTemplateRenderer("inapp", body, "text/plain", render.DefaultTitle)
}

func buildRetentionFn(repo storage.NotificationRepository, cfg config.NotifyConfig, logger *slog.Logger) func() {
	keep := cfg.HistoryKeep
	age := cfg.HistoryKeepFor
	if keep <= 0 && age <= 0 {
		return nil
	}
	return func() {
		if keep > 0 {
			if _, err := repo.PruneNotificationsByCount(keep); err != nil {
				logger.Warn("notify retention: prune by count failed", "error", err)
			}
		}
		if age > 0 {
			if _, err := repo.PruneNotificationsByAge(age); err != nil {
				logger.Warn("notify retention: prune by age failed", "error", err)
			}
		}
	}
}
