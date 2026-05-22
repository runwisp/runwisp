// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/tui"
)

// runDefault detects a running daemon or spawns one, then opens the TUI.
func runDefault() error {
	client := apiclient.NewUnix(localAPISocketPath())

	if client.HealthCheck() == nil {
		err := runTUIConnect(client)
		if err == nil {
			return nil
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
		return err
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

	return runTUIConnect(client)
}

// runTUIConnect launches the TUI against an already-healthy daemon client.
// The client is expected to already be wired (socket transport for local
// access; no Authenticate call is needed in that mode).
func runTUIConnect(client *apiclient.Client) error {
	info, err := client.GetDaemonInfo()
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info)

	// Fetch the ephemeral password over the local socket so the TUI's
	// "Password" home field can copy it to the clipboard. The env-var case
	// (ErrLocalCredentialsUnavailable) is expected, not an error — the
	// operator already knows the value they configured, and the TUI simply
	// hides the field.
	if creds, credErr := client.GetLocalCredentials(); credErr == nil && creds != nil {
		startupInfo.Password = creds.Password
		startupInfo.PasswordEphemeral = creds.Ephemeral
	} else if credErr != nil && !errors.Is(credErr, apiclient.ErrLocalCredentialsUnavailable) {
		slog.Warn("Could not fetch local credentials", "err", credErr)
	}

	_, tuiErr := tui.StartTUI(startupInfo, client, nil, shutdownDaemon, client.CreateLaunchTicket)
	if tuiErr != nil {
		return tuiErr
	}

	return nil
}
