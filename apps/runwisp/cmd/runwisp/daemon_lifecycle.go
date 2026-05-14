// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/cloud"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"log/slog"
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
		RunRepo:           &cloudRunRepo{inner: svc.DB},
		PendingUploadRepo: &cloudPendingUploadRepo{inner: svc.DB},
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

func awaitOrLog(ctx context.Context, wg *sync.WaitGroup, label string) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-ctx.Done():
		slog.Warn("graceful shutdown deadline exceeded", "phase", label)
	}
}

// runHeadless blocks until SIGINT/SIGTERM, then gracefully shuts down.
func runHeadless(cancelCloud context.CancelFunc, cloudWG *sync.WaitGroup, svc *daemonServices, srv *server.Server) error {
	slog.Info("Running in daemon mode (no TUI). Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("Shutting down...")
	gracefulShutdown(cancelCloud, cloudWG, svc, srv)
	slog.Info("Goodbye!")
	return nil
}

// runWithTUI starts the interactive TUI, waits for it to exit, then shuts down
// or transitions to headless mode depending on user choice.
func runWithTUI(
	srv *server.Server,
	svc *daemonServices,
	info uikit.StartupInfo,
	debugWriter *tui.DebugLogWriter,
	cancelCloud context.CancelFunc,
	cloudWG *sync.WaitGroup,
) error {
	client := apiclient.NewUnix(localAPISocketPath())
	if srv != nil {
		if err := pollHealth(client, 3*time.Second); err != nil {
			slog.Warn("Health check did not pass before TUI start", "err", err)
		}
	}

	if debugWriter != nil {
		tui.SetLogOutput(debugWriter)
	}

	shutdownFunc := func() error {
		gracefulShutdown(cancelCloud, cloudWG, svc, srv)
		return nil
	}

	var launchTicketFunc func() (string, error)
	if srv != nil {
		launchTicketFunc = client.CreateLaunchTicket
	}

	quitAction, tuiErr := tui.StartTUI(info, client, debugWriter, shutdownFunc, launchTicketFunc)
	if tuiErr != nil {
		tui.SetLogOutput(os.Stderr)
		slog.Warn("TUI exited with error", "err", tuiErr)
	}
	tui.SetLogOutput(os.Stderr)

	if quitAction == uikit.QuitKeepDaemon {
		slog.Info("TUI detached. Daemon running in background. Press Ctrl+C to stop.")
		return runHeadless(cancelCloud, cloudWG, svc, srv)
	}

	tui.PrintShutdownComplete()

	return nil
}
