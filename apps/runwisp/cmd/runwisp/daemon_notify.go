// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/channel"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/channel/inapp"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/coalesce"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/configload"
	"github.com/runwisp/runwisp/apps/runwisp/internal/notify/render"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// liveNotify owns the running notify.Service and swaps it when a reload changes
// the notification settings. The in-app Hub is created once and outlives every
// swap, so open notification streams stay attached across reloads.
type liveNotify struct {
	Hub *inapp.Hub

	mu      sync.Mutex
	service *notify.Service // nil when nothing is configured
	stopped bool

	// templates holds the template_path bodies the current service was built
	// from (see readNotifyTemplates), so a reload notices an edited template
	// even when runwisp.toml is unchanged. Written at boot and by a reload's
	// commit; the reconciler serialises reloads.
	templates map[string]string

	// retiring tracks services a swap replaced that are still draining, so Stop
	// can wait for them. cancelRetire cuts their drain short when the shutdown
	// deadline passes.
	retiring     sync.WaitGroup
	retireCtx    context.Context
	cancelRetire context.CancelFunc
}

func newLiveNotify() *liveNotify {
	ctx, cancel := context.WithCancel(context.Background())
	return &liveNotify{Hub: inapp.NewHub(32), retireCtx: ctx, cancelRetire: cancel}
}

// swap starts next (nil means no notifications) and retires the service it
// replaces. next subscribes before the old one detaches, both here, so only an
// event published between those two calls can be delivered twice, and none is
// dropped. The old service drains what it already accepted in the background
// under its own retry budget, sending any coalescing window it still holds as
// an early summary, and Stop waits for that. After Stop, swap is a
// no-op.
func (l *liveNotify) swap(next *notify.Service) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return
	}
	if next != nil {
		next.Start(context.Background())
	}
	old := l.service
	l.service = next
	if old != nil {
		old.Detach()
		l.retiring.Add(1)
		go func() {
			defer l.retiring.Done()
			defer crashguard.Guard()
			old.Stop(l.retireCtx)
		}()
	}
}

// Stop shuts the current service down for daemon exit and waits for any
// service a reload replaced to finish draining. Both are bounded by ctx: at its
// deadline, pending deliveries are cancelled. Nil-safe so callers that never
// wired notify can call it unconditionally.
func (l *liveNotify) Stop(ctx context.Context) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.stopped = true
	svc := l.service
	l.mu.Unlock()

	defer context.AfterFunc(ctx, l.cancelRetire)()
	if svc != nil {
		svc.Stop(ctx)
	}
	l.retiring.Wait()
}

// initNotify builds (but does not start) the notification service for cfg,
// delivering in-app notifications to hub. templates holds each notifier's
// template_path body, as read by readNotifyTemplates. Returns nil when there
// are no notifiers and no routes; the daemon then runs without notifications
// wired.
func initNotify(
	cfg *config.Config,
	templates map[string]string,
	fingerprint string,
	hub *inapp.Hub,
	db *storage.SQLiteDatabase,
	bus *events.Bus,
	logger *slog.Logger,
) (*notify.Service, error) {
	notifyCfg := cfg.Notify
	inappWanted := routesReferenceInapp(notifyCfg.Routes)
	if len(notifyCfg.Notifiers) == 0 && len(notifyCfg.Routes) == 0 && !inappWanted {
		return nil, nil
	}

	renderCtx := render.TemplateContext{
		ExternalURL: cfg.Daemon.ExternalURL,
		Fingerprint: fingerprint,
		OutputTail:  render.NewOutputTail(),
	}
	resolved := configload.Resolve(notifyCfg, renderCtx)
	// Resolve keeps the notifiers in config order.
	for i, n := range notifyCfg.Notifiers {
		resolved.Notifiers[i].Template = templates[n.TemplatePath]
	}

	// No notifiers and no rules: nothing to do. Skip every goroutine and the
	// bus subscription. The server's notification routes still respond from
	// the persistent repo, and the Hub stays attached for a later reload.
	if len(resolved.Notifiers) == 0 && len(resolved.Rules) == 0 {
		return nil, nil
	}

	if override := backoffOverride(notifyCfg.RetryBudget, logger); override != nil {
		for i := range resolved.Notifiers {
			t := override()
			resolved.Notifiers[i].Transport = t
			// SMTP/sendmail don't go through Transport at all, so the override
			// must also be threaded through as a plain BackoffConfig or
			// retry_budget silently has no effect on those channels.
			resolved.Notifiers[i].Backoff = t.Backoff
		}
	}

	channels := make([]notify.Channel, 0, len(resolved.Notifiers)+1)

	var inappCh *inapp.Channel
	if inappWanted {
		coalescerCfg := inapp.CoalescerConfig{
			// The in-app coalescer always applies a window: nil/zero falls back to
			// its built-in default. coalesce_window = "0s" only disables outbound.
			Window:        model.OrDefault(notifyCfg.CoalesceWindow, 0),
			CoalesceLimit: notifyCfg.CoalesceLimit,
		}
		coalescer := inapp.NewCoalescer(db, hub, time.Now, coalescerCfg, logger)

		inappRenderer, err := buildInappRenderer()
		if err != nil {
			return nil, err
		}

		inappCh = inapp.New("inapp", inappRenderer, coalescer)
		channels = append(channels, inappCh)
	}

	// Outbound coalescing is on unless the operator sets coalesce_window = "0s".
	// An omitted window (nil) or a positive value both keep it on; coalesce.New
	// applies its own 1h default when the window is zero.
	outboundCoalesce := notifyCfg.CoalesceWindow == nil || *notifyCfg.CoalesceWindow > 0
	coalesceCfg := coalesce.Config{
		Window:        model.OrDefault(notifyCfg.CoalesceWindow, 0),
		CoalesceLimit: notifyCfg.CoalesceLimit,
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
		return nil, err
	}
	channels = append(channels, outbound...)

	retentionFn := buildRetentionFn(db, notifyCfg, logger)

	return notify.New(notify.Config{
		Bus:         bus,
		Channels:    channels,
		Rules:       resolved.Rules,
		FailureSink: failureSink,
		Logger:      logger,
		RetentionFn: retentionFn,
	}), nil
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
		// The client's own per-request timeout must not outlive the budget: the retry
		// loop only checks MaxElapsedTime between attempts, so an unbounded (or
		// merely larger) per-request timeout lets one hanging request alone block
		// past a budget the operator asked for.
		if t.Client.Timeout > d {
			t.Client.Timeout = d
		}
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
	return render.NewTemplateRenderer("inapp", body, render.DefaultTitle, render.TemplateContext{})
}

func buildOutboundChannels(specs []channel.NotifierSpec, outboundCoalesce bool, coalesceCfg coalesce.Config, logger *slog.Logger, failureSink notify.SyntheticIngester) ([]notify.Channel, error) {
	channels := make([]notify.Channel, 0, len(specs))
	for _, spec := range specs {
		ch, err := channel.Build(spec)
		if err != nil {
			return nil, fmt.Errorf("build notifier %q: %w", spec.ID, err)
		}
		if outboundCoalesce {
			ch = coalesce.New(ch, coalesceCfg, time.Now, logger, failureSink)
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
