// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"os"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/runlog"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// startCloudClient creates and runs the cloud client in a background goroutine.
// Only called in cloud mode — the scheduler is nil, so no local scheduler teardown.
func startCloudClient(
	ctx context.Context,
	cfg *daemonConfig,
	svc *daemonServices,
) (context.CancelFunc, *sync.WaitGroup) {
	cloudCtx, cancelCloud := context.WithCancel(ctx)
	var cloudWG sync.WaitGroup

	if !cfg.CloudConfig.Enabled {
		return cancelCloud, &cloudWG
	}

	cloudClient, clientErr := cloud.NewClient(cfg.CloudConfig, cloud.Dependencies{
		TaskManager:       &cloudTaskRunner{inner: svc.TaskManager},
		RunRepo:           svc.DB,
		PendingUploadRepo: svc.DB,
		EventBus:          svc.EventBus,
		LocalTasks:        svc.TasksMap,
		LogDir:            flags.LogDir(),
		Availability:      svc.Executor.Availability(),
		Now:               time.Now,
		OnConnected: func() {
			slog.Info("Cloud connected")
		},
	})
	if clientErr != nil {
		slog.Error("Failed to create cloud client", "err", clientErr)
		return cancelCloud, &cloudWG
	}

	if cloudClient != nil {
		cloudClient.RecoverArchiveBacklog(cloudCtx)
		cloudWG.Add(1)
		go func() {
			defer cloudWG.Done()
			if runErr := cloudClient.Run(cloudCtx); runErr != nil {
				slog.Error("Cloud integration stopped", "err", runErr)
			}
		}()
	}

	return cancelCloud, &cloudWG
}

// gracefulShutdown tears down all daemon subsystems in two layers. The input
// layer (HTTP server, cloud connection) drains under a short fixed deadline
// so no new requests can enter; the worker layer (scheduler, notifications,
// task manager) drains under [daemon] shutdown_timeout so per-task graceful
// stop windows can complete. Both runHeadless and runWithTUI funnel through
// here.
func gracefulShutdown(cancelCloud context.CancelFunc, cloudWG *sync.WaitGroup, svc *daemonServices, srv *server.Server) {
	cancelCloud()
	// RetentionCleaner.Stop only cancels its loop's context — non-blocking,
	// so it's safe to call outside the deadline-bounded waits below.
	svc.RetentionCleaner.Stop()
	if svc.SoftDeletePurger != nil {
		svc.SoftDeletePurger.Stop()
	}

	inputCtx, cancelInput := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelInput()
	waitInput(inputCtx, cloudWG, srv)

	taskTimeout := svc.TaskShutdownTimeout
	if taskTimeout <= 0 {
		// Operator hasn't configured one — fall back to the historical 3s
		// to keep developer setups responsive.
		taskTimeout = 3 * time.Second
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), taskTimeout)
	defer cancelDrain()
	waitDrain(drainCtx, svc, taskTimeout)
}

// waitInput waits for the request-accepting layer to quiesce: HTTP server
// drains in-flight handlers; cloud client returns from Run after cloudCtx is
// cancelled. Once both return, downstream subsystems are no longer being
// called into and can be torn down without racing.
func waitInput(ctx context.Context, cloudWG *sync.WaitGroup, srv *server.Server) {
	slog.Info("draining HTTP requests and closing cloud connection")
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		cloudWG.Wait()
	}()

	if srv != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(ctx); err != nil {
				slog.Warn("HTTP server shutdown error", "err", err)
			}
		}()
	}

	awaitOrLog(ctx, &wg, "input layer")
}

// waitDrain stops worker subsystems. Order within is irrelevant — they don't
// call into each other — but they all share the global deadline. The task
// manager drain is bounded by taskTimeout so survivors get SIGKILLed and
// recorded as ReasonDaemonStopped if their per-task graceful_stop overruns.
func waitDrain(ctx context.Context, svc *daemonServices, taskTimeout time.Duration) {
	if inflight := inflightRunCount(svc); inflight > 0 {
		slog.Info("stopping scheduler and waiting for in-flight runs",
			"in_flight", inflight, "timeout", taskTimeout)
	} else {
		slog.Info("stopping scheduler", "timeout", taskTimeout)
	}

	var wg sync.WaitGroup

	if svc.Scheduler != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.Scheduler.Stop()
		}()
	}
	if svc.Notify.Service != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.Notify.Service.Stop(ctx); err != nil {
				slog.Warn("notification service shutdown error", "err", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.TaskManager.ShutdownWithDeadline(taskTimeout)
	}()

	awaitOrLog(ctx, &wg, "drain layer")
}

// inflightRunCount sums active runs across all tasks. Cheap enough at shutdown
// (one short read-locked count per task) to narrate what the drain is waiting
// on without adding a dedicated counter to the TaskManager interface.
func inflightRunCount(svc *daemonServices) int {
	total := 0
	for name := range svc.TasksMap {
		total += len(svc.TaskManager.GetActiveRuns(name))
	}
	return total
}

func awaitOrLog(ctx context.Context, wg *sync.WaitGroup, label string) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("graceful shutdown deadline exceeded", "phase", label)
	}
}

// daemonRuntime carries the long-lived daemon state that the headless and TUI
// shutdown paths both need: the pre-armed signal channel, the services bundle,
// the HTTP server, log routing handles, and the cloud cancel/wait pair.
// Bundling them keeps runHeadless / runWithTUI signatures narrow while leaving
// each field individually named.
type daemonRuntime struct {
	sigCh       <-chan os.Signal
	svc         *daemonServices
	srv         *server.Server
	debugWriter *tui.DebugLogWriter
	logBuffer   *server.DaemonLogBuffer
	cancelCloud context.CancelFunc
	cloudWG     *sync.WaitGroup
}

// runHeadless blocks until SIGINT/SIGTERM, then gracefully shuts down. The
// banner (or its non-TTY summary) already conveys "ready, listening" and, when
// no TUI is attached, a dim "Press Ctrl+C to stop." line — no slog cue here.
// sigCh is owned by runDaemon, which installs signal.Notify before any
// goroutine could self-signal — closing the race where a SIGTERM arriving
// during early init would terminate the process unhandled.
func runHeadless(rt *daemonRuntime) error {
	sig := <-rt.sigCh

	start := time.Now()
	slog.Info("received signal, shutting down", "signal", sig.String())
	gracefulShutdown(rt.cancelCloud, rt.cloudWG, rt.svc, rt.srv)
	slog.Info("shutdown complete", "elapsed", time.Since(start).Round(time.Millisecond))
	return nil
}

// runWithTUI starts the interactive TUI, waits for it to exit, then shuts down
// or transitions to headless mode depending on user choice.
func runWithTUI(rt *daemonRuntime, info uikit.StartupInfo) error {
	client := apiclient.NewUnix(localAPISocketPath())
	if rt.srv != nil {
		if err := pollHealth(client, 3*time.Second); err != nil {
			slog.Warn("Health check did not pass before TUI start", "err", err)
		}
	}

	if rt.debugWriter != nil {
		// Route slog into the TUI debug panel and the ring buffer (so remote
		// TUI clients see the same lines): plain text, no timestamps, and the
		// lipgloss color profile is left untouched (the TUI owns the screen).
		// The panel is named "Debug" and described as "Internal events and
		// diagnostics" — operators who open it want full verbosity, including
		// the HTTP access lines that the headless stream demotes to DEBUG.
		// So pin the slog level to DEBUG here regardless of --log-level; the
		// flag still governs the headless / JSON output, which is the
		// public-facing surface.
		clilog.Configure(clilog.Options{
			Level:   slog.LevelDebug,
			Format:  clilog.FormatText,
			Output:  io.MultiWriter(rt.debugWriter, rt.logBuffer),
			TUIMode: true,
		})
	}

	shutdownFunc := func() error {
		gracefulShutdown(rt.cancelCloud, rt.cloudWG, rt.svc, rt.srv)
		return nil
	}

	var launchTicketFunc func() (string, error)
	if rt.srv != nil {
		launchTicketFunc = client.CreateLaunchTicket
	}

	quitAction, tuiErr := tui.StartTUI(info, client, rt.debugWriter, shutdownFunc, launchTicketFunc)
	clilog.SetOutput(io.MultiWriter(os.Stderr, rt.logBuffer))
	if tuiErr != nil {
		slog.Warn("TUI exited with error", "err", tuiErr)
	}

	if quitAction == uikit.QuitKeepDaemon {
		// The panel is gone; restore full daemon-mode logging (timestamps,
		// color gating) and narrate run lifecycle to stderr like a headless
		// boot would. The ring buffer stays tee'd so remote TUI clients still
		// see daemon logs.
		clilog.Configure(clilog.Options{
			Level:      flags.LogLevel,
			Format:     flags.LogFormat,
			Output:     io.MultiWriter(os.Stderr, rt.logBuffer),
			DaemonMode: true,
		})
		defer runlog.Subscribe(rt.svc.EventBus)()
		slog.Info("TUI detached. Daemon running in background. Press Ctrl+C to stop.")
		return runHeadless(rt)
	}

	tui.PrintShutdownComplete()

	return nil
}
