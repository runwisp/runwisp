// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runlog"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/station"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// startStationClient creates and runs the station client in a background goroutine.
// Only called in station mode — the scheduler is nil, so no local scheduler teardown.
func startStationClient(
	ctx context.Context,
	cfg *daemonConfig,
	svc *daemonServices,
	srv *server.Server,
) (context.CancelFunc, *sync.WaitGroup) {
	stationCtx, cancelStation := context.WithCancel(ctx)
	var stationWG sync.WaitGroup

	if !cfg.StationConfig.Enabled {
		return cancelStation, &stationWG
	}

	stationClient, clientErr := station.NewClient(cfg.StationConfig, station.Dependencies{
		TaskManager:       &stationTaskRunner{stationRuntime: svc.TaskManager},
		RunRepo:           svc.DB,
		PendingUploadRepo: svc.DB,
		EventBus:          svc.EventBus,
		LocalTasks:        svc.Tasks,
		LogDir:            flags.LogDir(),
		Availability:      svc.Executor.Availability(),
		Now:               time.Now,
		OnConnected: func() {
			slog.Info("Station connected")
		},
		RequestRestart: func() error { return requestSelfRestart(cfg.Config.Daemon.AllowStationDispatch) },
		SystemStats:    srv.SystemStats,
	})
	if clientErr != nil {
		slog.Error("Failed to create station client", "err", clientErr)
		return cancelStation, &stationWG
	}

	if stationClient != nil {
		stationWG.Add(1)
		go func() {
			defer crashguard.Guard()
			defer stationWG.Done()
			// Backlog recovery does real HTTP PUTs (up to 90s each) against the
			// Station peer; running it here (not before srv.Start) means a slow or
			// unreachable peer can never delay the UI/API from becoming
			// available. stationCtx cancellation (on shutdown) unblocks it the same
			// way it unblocks Run below.
			stationClient.RecoverArchiveBacklog(stationCtx)
			if runErr := stationClient.Run(stationCtx); runErr != nil {
				slog.Error("Station integration stopped", "err", runErr)
			}
		}()
	}

	return cancelStation, &stationWG
}

// requestSelfRestart honours a Station agent:restart by delivering SIGTERM to
// this process, which the signal handler turns into the same graceful shutdown
// a `runwisp stop` would — and the service manager then brings the daemon back.
// It is refused when the daemon is not service-managed, since exiting would
// then stop the agent for good rather than restart it, and when the operator
// hasn't opted into station dispatch — restarting the daemon process is at least
// as sensitive as the ad-hoc task execution that flag already gates.
func requestSelfRestart(allowStationDispatch bool) error {
	if !allowStationDispatch {
		return fmt.Errorf("station dispatch disabled (set [daemon] allow_station_dispatch = true to enable)")
	}
	if !autostart.RunningUnderServiceManager() {
		return fmt.Errorf("daemon is not managed by a service manager; restart it manually")
	}
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return fmt.Errorf("locate own process: %w", err)
	}
	slog.Info("restarting agent on station request; service manager will bring it back")
	if err := p.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal self for restart: %w", err)
	}
	return nil
}

// gracefulShutdown tears down all daemon subsystems in two layers. The input
// layer (HTTP server, station connection) drains under a short fixed deadline
// so no new requests can enter; the worker layer (scheduler, notifications,
// task manager) drains under [daemon] shutdown_timeout so per-task graceful
// stop windows can complete. Both runHeadless and runWithTUI funnel through
// here.
func gracefulShutdown(cancelStation context.CancelFunc, stationWG *sync.WaitGroup, svc *daemonServices, srv *server.Server) {
	cancelStation()
	// Abort any depends_on launcher still waiting on a dependency so it can't
	// start a service mid-teardown.
	if svc.ServiceLaunchCancel != nil {
		svc.ServiceLaunchCancel()
	}
	// RetentionCleaner.Stop only cancels its loop's context — non-blocking,
	// so it's safe to call outside the deadline-bounded waits below.
	svc.RetentionCleaner.Stop()
	if svc.SoftDeletePurger != nil {
		svc.SoftDeletePurger.Stop()
	}
	if svc.MemoryReclaimer != nil {
		svc.MemoryReclaimer.Stop()
	}
	// nil receiver when the pprof endpoint was never enabled — Close handles it.
	svc.DebugServer.Close()

	inputCtx, cancelInput := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelInput()
	waitInput(inputCtx, stationWG, srv)

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
// drains in-flight handlers; station client returns from Run after stationCtx is
// cancelled. Once both return, downstream subsystems are no longer being
// called into and can be torn down without racing.
func waitInput(ctx context.Context, stationWG *sync.WaitGroup, srv *server.Server) {
	slog.Info("draining HTTP requests and closing station connection")
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		stationWG.Wait()
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

	// Tear services down in reverse-dependency order before the bulk drain, so
	// a dependent stops before the services it relies on. ShutdownWithDeadline
	// below stays the safety net for cron tasks and anything that didn't drain.
	preStopServices(ctx, svc)

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
	for name := range svc.Tasks.Snapshot() {
		total += svc.TaskManager.GetActiveRunCount(name)
	}
	return total
}

// preStopServices stops services in reverse-dependency order (dependents
// first), giving each a chance to drain before moving on. The whole pass shares
// ctx's deadline; whatever doesn't finish here is caught by the subsequent
// ShutdownWithDeadline safety net. Services-only — cron tasks are left to the
// bulk drain.
func preStopServices(ctx context.Context, svc *daemonServices) {
	for _, task := range orderServicesForStop(svc.Tasks.Snapshot()) {
		if ctx.Err() != nil {
			return
		}
		if err := svc.TaskManager.StopService(task.Name); err != nil {
			slog.Warn("failed to stop service during shutdown", "task", task.Name, "err", err)
			continue
		}
		waitServiceDrained(ctx, svc, task.Name)
	}
}

// waitServiceDrained blocks until a service has no active runs or ctx expires.
func waitServiceDrained(ctx context.Context, svc *daemonServices, name string) {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if svc.TaskManager.GetActiveRunCount(name) == 0 {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
// the HTTP server, log routing handles, and the station cancel/wait pair.
// Bundling them keeps runHeadless / runWithTUI signatures narrow while leaving
// each field individually named.
type daemonRuntime struct {
	sigCh         <-chan os.Signal
	svc           *daemonServices
	srv           *server.Server
	fatalCh       chan error
	reload        func() (model.ReloadResult, error)
	debugWriter   *tui.DebugLogWriter
	logBuffer     *server.DaemonLogBuffer
	cancelStation context.CancelFunc
	stationWG     *sync.WaitGroup
}

// runHeadless blocks until SIGINT/SIGTERM, then gracefully shuts down. The
// banner (or its non-TTY summary) already conveys "ready, listening" and, when
// no TUI is attached, a dim "Press Ctrl+C to stop." line — no slog cue here.
// sigCh is owned by runDaemon, which installs signal.Notify before any
// goroutine could self-signal — closing the race where a SIGTERM arriving
// during early init would terminate the process unhandled.
func runHeadless(rt *daemonRuntime) error {
	for sig := range rt.sigCh {
		if sig == syscall.SIGHUP {
			handleReloadSignal(rt)
			continue
		}

		start := time.Now()
		// A fatal server error self-signals SIGTERM (superviseServerStart); when
		// that's the cause, log it as such instead of implying a phantom
		// external signal.
		if ferr := readFatal(rt.fatalCh); ferr != nil {
			slog.Error("shutting down due to fatal server error", "err", ferr)
		} else {
			slog.Info("received signal, shutting down", "signal", sig.String())
		}
		gracefulShutdown(rt.cancelStation, rt.stationWG, rt.svc, rt.srv)
		slog.Info("shutdown complete", "elapsed", time.Since(start).Round(time.Millisecond))
		return nil
	}
	return nil
}

// readFatal non-blockingly reads a fatal server error if one is pending. Nil
// means the shutdown was triggered by a genuine external signal, not a
// self-signalled server failure.
func readFatal(ch <-chan error) error {
	if ch == nil {
		return nil
	}
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

// handleReloadSignal services a SIGHUP by reconciling runwisp.toml against the
// live task set. A reload failure (bad config or a restart-only change) is
// logged and the daemon keeps running on its current set — SIGHUP never tears
// the process down. nil reconciler (station mode) makes this a logged no-op.
func handleReloadSignal(rt *daemonRuntime) {
	if rt.reload == nil {
		slog.Warn("received SIGHUP but reload is not available in this mode")
		return
	}
	slog.Info("received SIGHUP, reloading configuration")
	result, err := rt.reload()
	if err != nil {
		slog.Error("reload rejected", "err", err)
		return
	}
	slog.Info("reload applied",
		"added", len(result.Added), "changed", len(result.Changed), "removed", len(result.Removed))
}

// runWithTUI starts the interactive TUI, waits for it to exit, then shuts down
// or transitions to headless mode depending on user choice.
func runWithTUI(rt *daemonRuntime, info uikit.StartupInfo, f Flags) error {
	client := apiclient.NewUnix(localAPISocketPath(f))
	if rt.srv != nil {
		// Wait for the listeners to bind first so a slow boot doesn't trip a
		// spurious health-check warning (same race emitReadiness guards).
		waitServerReady(rt, serverReadyTimeout)
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
		gracefulShutdown(rt.cancelStation, rt.stationWG, rt.svc, rt.srv)
		return nil
	}

	var launchTicketFunc func() (string, error)
	if rt.srv != nil {
		launchTicketFunc = func() (string, error) {
			return client.CreateLaunchTicket(context.Background())
		}
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
			Level:      f.LogLevel,
			Format:     f.LogFormat,
			Output:     io.MultiWriter(os.Stderr, clilog.NewPlainWriter(rt.logBuffer)),
			DaemonMode: true,
		})
		defer runlog.Subscribe(rt.svc.EventBus)()
		slog.Info("TUI detached. Daemon running in background. Press Ctrl+C to stop.")
		return runHeadless(rt)
	}

	tui.PrintShutdownComplete()

	return nil
}
