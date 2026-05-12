// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/tui"
	"log/slog"
)

// runDefault detects a running daemon or spawns one, then opens the TUI.
//
// Authentication uses the local-JWT shortcut: the CLI reads the daemon's
// JWT signing secret straight off disk (the same trust boundary that
// already lets us read everything else in the data dir) and mints a
// short-lived token. No password prompt, no SRP roundtrip.
func runDefault() error {
	client, err := newLocalAuthedClient()
	if err != nil {
		return err
	}

	if client.HealthCheck() == nil {
		tuiErr := runTUIConnect(client)
		if tuiErr == nil {
			return nil
		}
		if errors.Is(tuiErr, apiclient.ErrUnauthorized) {
			return passwordMismatchError(flags.Port)
		}
		if errors.Is(tuiErr, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
		return tuiErr
	}

	// Health check failed — we're about to spawn a daemon. Before that, if
	// there's no runwisp.toml and we're at a terminal, offer to create one.
	// A spawned background daemon has no TTY, so the prompt must happen
	// here in the foreground process.
	if err := scaffoldIfMissing(flags.CfgFile); err != nil {
		return err
	}

	// Before paying the cost of spawning a background daemon (which would
	// then silently fail to bind), probe the port ourselves. If something
	// is holding it but it is not a RunWisp daemon we can surface a clear,
	// actionable error immediately.
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

	// The freshly spawned daemon may have rotated the JWT secret on its
	// first boot (schema_version bump). Re-mint so we authenticate with
	// the current secret instead of the one we read before the spawn.
	client, err = newLocalAuthedClient()
	if err != nil {
		return err
	}
	return runTUIConnect(client)
}

// newLocalAuthedClient returns an apiclient pre-loaded with a freshly-minted
// local JWT. Callers must not assume the token is long-lived (5 min TTL).
func newLocalAuthedClient() (*apiclient.Client, error) {
	token, err := mintLocalJWT(flags.DBPath())
	if err != nil {
		return nil, fmt.Errorf("mint local JWT: %w", err)
	}
	return apiclient.New(localAPIBaseURL(), "", apiclient.WithBearerToken(token)), nil
}

// runTUIConnect launches the TUI as a client connected to a running daemon.
func runTUIConnect(client *apiclient.Client) error {
	info, err := client.GetDaemonInfo()
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info)

	_, tuiErr := tui.StartTUI(startupInfo, client, nil, shutdownDaemon, client.CreateLaunchTicket)
	if tuiErr != nil {
		return tuiErr
	}

	return nil
}
