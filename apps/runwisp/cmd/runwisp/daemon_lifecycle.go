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
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/tui"
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
		TaskManager:  svc.TaskManager,
		RunRepo:      svc.DB,
		EventBus:     svc.EventBus,
		LocalTasks:   svc.TasksMap,
		LogDir:       flags.LogDir(),
		Availability: svc.Executor.Availability(),
		OnConnected: func() {
			slog.Info("Cloud connected")
		},
	})
	if clientErr != nil {
		slog.Error("Failed to create cloud client", "err", clientErr)
		return cancelCloud, &cloudWG
	}

	if cloudClient != nil {
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

// runHeadless blocks until SIGINT/SIGTERM, then gracefully shuts down.
func runHeadless(cancelCloud context.CancelFunc, cloudWG *sync.WaitGroup, scheduler *runtime.Scheduler, jm runtime.TaskManager) error {
	slog.Info("Running in daemon mode (no TUI). Press Ctrl+C to stop.")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	slog.Info("Shutting down...")
	cancelCloud()
	cloudWG.Wait()
	if scheduler != nil {
		scheduler.Stop()
	}
	jm.Shutdown()
	slog.Info("Goodbye!")
	return nil
}

// runWithTUI starts the interactive TUI, waits for it to exit, then shuts down
// or transitions to headless mode depending on user choice.
func runWithTUI(
	srv *server.Server,
	svc *daemonServices,
	info tui.StartupInfo,
	debugWriter *tui.DebugLogWriter,
	cancelCloud context.CancelFunc,
	cloudWG *sync.WaitGroup,
) error {
	if srv != nil {
		localClient := apiclient.New(localAPIBaseURL(), "")
		if err := pollHealth(localClient, 3*time.Second); err != nil {
			slog.Warn("Health check did not pass before TUI start", "err", err)
		}
	}

	client := apiclient.New(localAPIBaseURL(), info.Password)
	if authErr := client.Authenticate(); authErr != nil {
		slog.Warn("TUI auth failed", "err", authErr)
	}

	if debugWriter != nil {
		tui.SetLogOutput(debugWriter)
	}

	shutdownFunc := func() error {
		cancelCloud()
		cloudWG.Wait()
		svc.RetentionCleaner.Stop()
		if svc.Scheduler != nil {
			svc.Scheduler.Stop()
		}
		svc.TaskManager.Shutdown()
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

	if quitAction == tui.QuitKeepDaemon {
		slog.Info("TUI detached. Daemon running in background. Press Ctrl+C to stop.")
		return runHeadless(cancelCloud, cloudWG, svc.Scheduler, svc.TaskManager)
	}

	tui.PrintShutdownComplete()

	return nil
}
