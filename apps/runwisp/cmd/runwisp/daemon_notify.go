// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/internal/notify/coalesce"
	"github.com/runwisp/runwisp/internal/notify/configload"
	"github.com/runwisp/runwisp/internal/notify/render"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
)

// notifyBundle owns the runtime objects the rest of the daemon needs after
// notify is wired: the Service (for lifecycle) and the inapp Hub (for the SSE
// handler in internal/server). Hub may be nil when no route targets the
// inapp channel.
type notifyBundle struct {
	Service *notify.Service
	Hub     *inapp.Hub
}

// serverHub exposes the in-app Hub as the server's NotificationHub, returning an
// explicit nil interface when no Hub was built. This avoids the typed-nil
// footgun: a nil *inapp.Hub assigned straight into the interface wraps a nil
// pointer in a non-nil interface, slipping past the server's `notifyHub == nil`
// check and panicking the notifications SSE stream when it calls Subscribe().
func (b notifyBundle) serverHub() server.NotificationHub {
	if b.Hub == nil {
		return nil
	}
	return b.Hub
}

// initNotify constructs the notification subsystem from the daemon config.
// Returns a zero bundle (Service nil) when there are no notifiers and no
// routes — the daemon then runs without notifications wired at all.
func initNotify(
	cfg *daemonConfig,
	db storage.Database,
	bus *events.Bus,
	tasksMap map[string]*model.Task,
	logger *slog.Logger,
) (notifyBundle, error) {
	notifyCfg := cfg.Config.Notify
	inappWanted := routesReferenceInapp(notifyCfg.Routes)
	if len(notifyCfg.Notifiers) == 0 && len(notifyCfg.Routes) == 0 && !inappWanted {
		return notifyBundle{}, nil
	}

	renderCtx := render.TemplateContext{
		ExternalURL: cfg.Config.Daemon.ExternalURL,
		Fingerprint: cfg.Fingerprint,
		OutputTail:  render.NewOutputTail(),
	}
	resolved, err := configload.Resolve(notifyCfg, renderCtx)
	if err != nil {
		return notifyBundle{}, fmt.Errorf("resolve notify config: %w", err)
	}

	// No notifiers and no rules: nothing to do. Skip every goroutine and the
	// bus subscription. The server's notification routes still respond from
	// the persistent repo; the SSE stream falls back to ping-only.
	if len(resolved.Notifiers) == 0 && len(resolved.Rules) == 0 {
		return notifyBundle{}, nil
	}

	if override := backoffOverride(notifyCfg.RetryBudget, logger); override != nil {
		for i := range resolved.Notifiers {
			resolved.Notifiers[i].Transport = override()
		}
	}

	channels := make([]notify.Channel, 0, len(resolved.Notifiers)+1)

	var hub *inapp.Hub
	var inappCh *inapp.Channel
	if inappWanted {
		hub = inapp.NewHub(32)

		coalescerCfg := inapp.CoalescerConfig{
			Window:      notifyCfg.CoalesceWindow,
			OccurrenceN: notifyCfg.KeepOccurrences,
		}
		coalescer := inapp.NewCoalescer(db, hub, notify.RealClock(), coalescerCfg, logger)

		inappRenderer, err := buildInappRenderer()
		if err != nil {
			return notifyBundle{}, err
		}

		inappCh = inapp.New("inapp", inappRenderer, coalescer)
		channels = append(channels, inappCh)
	}

	outboundCoalesce := notifyCfg.CoalesceOutbound
	coalesceCfg := coalesce.Config{
		Window: notifyCfg.CoalesceWindow,
		EveryN: notifyCfg.KeepOccurrences,
	}

	// The in-app channel is the failure sink for permanently-failed outbound
	// deliveries. Compute it before building the outbound channels so a
	// coalesce-wrapped channel can surface its async window-close failures
	// through it too (not just the dispatcher's synchronous path).
	var failureSink notify.SyntheticIngester
	if inappCh != nil {
		failureSink = inappCh
	}

	outbound, err := buildOutboundChannels(resolved.Notifiers, outboundCoalesce, coalesceCfg, logger, failureSink)
	if err != nil {
		return notifyBundle{}, err
	}
	channels = append(channels, outbound...)

	retentionFn := buildRetentionFn(db, notifyCfg, logger)

	svc := notify.New(notify.Config{
		Bus:              bus,
		Channels:         channels,
		Rules:            resolved.Rules,
		FailureSink:      failureSink,
		Logger:           logger,
		RetentionEvery:   5 * time.Minute,
		RetentionFn:      retentionFn,
		MutedMissedTasks: mutedMissedTasks(tasksMap),
	})

	return notifyBundle{Service: svc, Hub: hub}, nil
}

// syncMutedMissed refreshes the notify service's per-task missed-run mute set
// from the live task registry. Called after every successful reload (SIGHUP
// and POST /api/daemon/reload both route through this) so a task's
// treat_missed_as_failure change takes effect immediately instead of only
// after a full daemon restart.
func syncMutedMissed(svc *daemonServices) {
	if svc.Notify.Service == nil {
		return
	}
	svc.Notify.Service.SetMutedMissed(mutedMissedTasks(svc.Tasks.Snapshot()))
}

// mutedMissedTasks collects the names of tasks with treat_missed_as_failure = false.
// The notify Service drops run.missed events for these at ingress, leaving the
// browsable missed run row untouched. Returns nil when none are muted so the
// common case allocates nothing. Takes a name-keyed map so both the boot path
// (built from cfg.Config.Tasks) and the reload path (the live TaskRegistry
// snapshot) can share it.
func mutedMissedTasks(tasks map[string]*model.Task) map[string]struct{} {
	var muted map[string]struct{}
	for _, t := range tasks {
		if t.NotifiesOnMissed() {
			continue
		}
		if muted == nil {
			muted = make(map[string]struct{})
		}
		muted[t.Name] = struct{}{}
	}
	return muted
}

// backoffOverride returns a transport-builder that shrinks the outbound retry
// budget when retry_budget is set in TOML. A separate transport is
// constructed per-channel so per-channel Body429Fn customisation still applies.
func backoffOverride(d time.Duration, logger *slog.Logger) func() *notify.HTTPProvider {
	if d <= 0 {
		return nil
	}
	logger.Info("notify backoff overridden via retry_budget", "max_elapsed", d)
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

// routesReferenceInapp reports whether any resolved route targets the
// special "inapp" channel. The inapp Hub and Channel only need to exist if
// something is actually going to be delivered to them.
func routesReferenceInapp(routes []config.NotificationRoute) bool {
	for _, r := range routes {
		for _, id := range r.NotifierID {
			if id == "inapp" {
				return true
			}
		}
	}
	return false
}

func buildInappRenderer() (render.Renderer, error) {
	body, err := render.LoadDefaultTemplate("inapp")
	if err != nil {
		return nil, fmt.Errorf("load inapp template: %w", err)
	}
	return render.NewTemplateRenderer("inapp", body, "text/plain", render.DefaultTitle)
}

func buildOutboundChannels(specs []channel.NotifierSpec, outboundCoalesce bool, coalesceCfg coalesce.Config, logger *slog.Logger, failureSink notify.SyntheticIngester) ([]notify.Channel, error) {
	channels := make([]notify.Channel, 0, len(specs))
	for _, spec := range specs {
		ch, err := channel.Build(spec)
		if err != nil {
			return nil, fmt.Errorf("build notifier %q: %w", spec.ID, err)
		}
		if outboundCoalesce {
			ch = coalesce.New(ch, coalesceCfg, notify.RealClock(), logger, failureSink)
		}
		channels = append(channels, ch)
	}
	return channels, nil
}

func buildRetentionFn(repo storage.NotificationRepository, cfg config.NotifyConfig, logger *slog.Logger) func(context.Context) {
	keep := cfg.KeepNotifications
	age := cfg.KeepFor
	if keep <= 0 && age <= 0 {
		return nil
	}
	return func(ctx context.Context) {
		if keep > 0 {
			if _, err := repo.PruneNotificationsByCount(ctx, keep); err != nil {
				logger.Warn("notify retention: prune by count failed", "error", err)
			}
		}
		if age > 0 {
			if _, err := repo.PruneNotificationsByAge(ctx, age); err != nil {
				logger.Warn("notify retention: prune by age failed", "error", err)
			}
		}
	}
}
