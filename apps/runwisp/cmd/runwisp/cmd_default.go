// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/tui"
)

// runDefault detects a running daemon or spawns one, then opens the TUI.
func runDefault(f Flags) error {
	client := apiclient.NewUnix(localAPISocketPath(f))

	if client.HealthCheck() == nil {
		err := runTUIConnect(client, f)
		if err == nil {
			return nil
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(f.Port)
		}
		return err
	}

	// Health check failed — we're about to spawn a daemon. Before that, if
	// there's no runwisp.toml and we're at a terminal, offer to create one.
	// A spawned background daemon has no TTY, so the prompt must happen
	// here in the foreground process.
	if err := scaffoldIfMissing(f.CfgFile); err != nil {
		return err
	}

	// Before paying the cost of spawning a background daemon (which would then
	// silently fail to bind), probe the port ourselves. If a RunWisp daemon
	// from a different datadir holds it, offer to connect to or stop it;
	// otherwise surface a clear, actionable port-conflict error.
	if bindErr := probePortAvailable(f.Host, f.Port); bindErr != nil {
		info := probeRunwispInstance(f.Host, f.Port)
		interactive := isatty.IsTerminal(os.Stdin.Fd())
		choice, resErr := resolvePortConflict(f, bindErr, info, interactive, os.Stdin, os.Stderr)
		if resErr != nil {
			return resErr
		}
		switch choice {
		case conflictConnect:
			return runTUIConnect(apiclient.NewUnix(info.SocketPath), f)
		case conflictStopAndLaunch:
			if stopErr := stopConflictingDaemon(f, info); stopErr != nil {
				return stopErr
			}
			// Other daemon stopped; fall through to spawn our own here.
		case conflictAbort:
			return nil
		}
	}

	if err := spawnDaemon(f); err != nil {
		slog.Warn("Failed to spawn background daemon, running inline", "err", err)
		return runDaemon(modeStandalone, f, false)
	}

	logPath := filepath.Join(f.DataDir, "daemon.log")
	if err := waitForDaemon(client, logPath, 10*time.Second, f); err != nil {
		return err
	}

	return runTUIConnect(client, f)
}

// runTUIConnect launches the TUI against an already-healthy daemon client.
// The client is expected to already be wired (socket transport for local
// access; no Authenticate call is needed in that mode).
func runTUIConnect(client *apiclient.Client, f Flags) error {
	info, err := client.GetDaemonInfo()
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info)

	// Fetch the ephemeral password over the local socket so the TUI's
	// "Password" home field can copy it to the clipboard. The env-var case
	// (ErrLocalCredentialsUnavailable) and the RUNWISP_NO_AUTH case
	// (ErrAuthDisabled) are expected, not errors — the TUI hides or replaces
	// the field based on the daemon info it already fetched.
	if creds, credErr := client.GetLocalCredentials(); credErr == nil && creds != nil {
		startupInfo.Password = creds.Password
		startupInfo.PasswordEphemeral = creds.Ephemeral
	} else if credErr != nil &&
		!errors.Is(credErr, apiclient.ErrLocalCredentialsUnavailable) &&
		!errors.Is(credErr, apiclient.ErrAuthDisabled) {
		slog.Warn("Could not fetch local credentials", "err", credErr)
	}

	shutdownFunc := func() error { return shutdownDaemon(f) }
	_, tuiErr := tui.StartTUI(startupInfo, client, nil, shutdownFunc, client.CreateLaunchTicket)
	if tuiErr != nil {
		return tuiErr
	}

	return nil
}
