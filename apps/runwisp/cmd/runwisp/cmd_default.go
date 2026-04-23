// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/tui"
	"log/slog"
)

// runDefault detects a running daemon or spawns one, then opens the TUI.
func runDefault() error {
	password, _, err := datadir.ResolvePassword(flags.DataDir)
	if err != nil {
		return err
	}

	client := apiclient.New(localAPIBaseURL(), password)

	passwordExplicit := os.Getenv("RUNWISP_PASSWORD") != ""

	if client.HealthCheck() == nil {
		err := runTUIConnect(client, password, passwordExplicit)
		if err == nil {
			return nil
		}
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return passwordMismatchError(flags.Port)
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
		return err
	}

	// Health check failed. Before paying the cost of spawning a background
	// daemon (which would then silently fail to bind), probe the port
	// ourselves. If something is holding it but it is not a RunWisp daemon
	// we can surface a clear, actionable error immediately.
	if bindErr := probePortAvailable(flags.Host, flags.Port); bindErr != nil {
		return portConflictError(flags.Host, flags.Port, bindErr)
	}

	if err := spawnDaemon(); err != nil {
		slog.Warn("Failed to spawn background daemon, running inline", "err", err)
		return runDaemon(modeStandalone)
	}

	logPath := filepath.Join(flags.DataDir, "daemon.log")
	if err := waitForDaemon(client, logPath, 10*time.Second); err != nil {
		return err
	}

	return runTUIConnect(client, password, passwordExplicit)
}

// runTUIConnect launches the TUI as a client connected to a running daemon.
// passwordExplicit indicates the user supplied the password via env/flag
// (meaning we should NOT disclose it in the UI).
func runTUIConnect(client *apiclient.Client, password string, passwordExplicit bool) error {
	if err := client.Authenticate(); err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}

	info, err := client.GetDaemonInfo()
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info, password, passwordExplicit)

	_, tuiErr := tui.StartTUI(startupInfo, client, nil, shutdownDaemon, client.CreateLaunchTicket)
	if tuiErr != nil {
		return tuiErr
	}

	return nil
}
