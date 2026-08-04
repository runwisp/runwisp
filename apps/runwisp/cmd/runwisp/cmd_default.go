// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
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

	// Spawn a background daemon or handle port conflicts.
	serviceInstalled, err := scaffoldIfMissing(f)
	if err != nil {
		return err
	}

	// When first run performed the cron cutover, systemd already owns a daemon on
	// this data dir and started it — spawning a second one here would fight it for
	// the port and the SQLite file. Wait for the one systemd started (which is a
	// health poll, not a spawn) and attach to it.
	logPath := filepath.Join(f.DataDir, "daemon.log")
	if serviceInstalled {
		if err := waitForDaemon(client, logPath, 30*time.Second, f); err != nil {
			return err
		}
		return runTUIConnect(client, f)
	}

	spawn, portErr := ensurePortFreeOrHandle(f)
	if portErr != nil || !spawn {
		return portErr
	}

	if err := spawnDaemon(f); err != nil {
		slog.Warn("Failed to spawn background daemon, running inline", "err", err)
		return runDaemon(modeStandalone, f, false)
	}

	if err := waitForDaemon(client, logPath, 10*time.Second, f); err != nil {
		return err
	}

	return runTUIConnect(client, f)
}

// ensurePortFreeOrHandle probes the bind port before a spawn.
// Returns spawn=true when the caller should launch its own daemon.
func ensurePortFreeOrHandle(f Flags) (spawn bool, err error) {
	bindErr := probePortAvailable(f.Host, f.Port)
	if bindErr == nil {
		return true, nil
	}

	info := probeRunwispInstance(f.Host, f.Port)
	interactive := isatty.IsTerminal(os.Stdin.Fd())
	choice, resErr := resolvePortConflict(f, bindErr, info, interactive, os.Stdin, os.Stderr)
	if resErr != nil {
		return false, resErr
	}
	switch choice {
	case conflictConnect:
		return false, runTUIConnect(apiclient.NewUnix(info.SocketPath), f)
	case conflictStopAndLaunch:
		if stopErr := stopConflictingDaemon(f, info); stopErr != nil {
			return false, stopErr
		}
		return true, nil // other daemon stopped; spawn our own
	default: // conflictAbort
		return false, nil
	}
}

// runTUIConnect launches the TUI against a local daemon via Unix socket.
func runTUIConnect(client *apiclient.Client, f Flags) error {
	return launchConnectedTUI(client, tuiConnectMode{
		shutdownFunc: func() error { return shutdownDaemon(f) },
	})
}

// tuiConnectMode carries the few things that differ between attaching the TUI
// to a local daemon (Unix socket) versus a remote daemon (authenticated HTTP).
type tuiConnectMode struct {
	// remote is true when the client talks HTTP to a daemon we don't share a
	// data dir with. It selects the Web UI base URL and suppresses local-only
	// affordances (ephemeral-password copy, daemon shutdown).
	remote bool
	// connBaseURL is the URL the remote client connected to; it is the Web UI
	// base when the daemon declares no external_url. Empty for local socket.
	connBaseURL string
	// shutdownFunc, when non-nil, lets the quit dialog stop the daemon. Nil for
	// remote — a local PID file can't kill a daemon on another host.
	shutdownFunc func() error
}

// launchConnectedTUI is the shared tail for local socket and remote HTTP paths.
// Pulls daemon info, resolves Web UI base URL, fills in transport-specific bits, and runs TUI.
func launchConnectedTUI(client *apiclient.Client, mode tuiConnectMode) error {
	// The TUI needs a real terminal; without one it would hang on stdin. Decline
	// clearly instead. Any spawned background daemon keeps running headless.
	if !isInteractiveTerminal() {
		return errors.New("no interactive terminal; the daemon runs headless here — use 'runwisp cloud' / 'runwisp daemon', or run 'runwisp tui' from a real terminal")
	}

	info, err := client.GetDaemonInfo()
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info)
	startupInfo.ListenURL = resolveTUIListenURL(info, mode)

	if !mode.remote {
		// Fetch the ephemeral password over the local socket so the TUI's
		// "Password" home field can copy it to the clipboard. The env-var case
		// (ErrLocalCredentialsUnavailable) and the RUNWISP_NO_AUTH case
		// (ErrAuthDisabled) are expected, not errors — the TUI hides or replaces
		// the field based on the daemon info it already fetched. A remote
		// operator already entered the password and the endpoint is local-only,
		// so we skip it there.
		if creds, credErr := client.GetLocalCredentials(); credErr == nil && creds != nil {
			startupInfo.Password = creds.Password
			startupInfo.PasswordEphemeral = creds.Ephemeral
		} else if credErr != nil &&
			!errors.Is(credErr, apiclient.ErrLocalCredentialsUnavailable) &&
			!errors.Is(credErr, apiclient.ErrAuthDisabled) {
			slog.Warn("Could not fetch local credentials", "err", credErr)
		}
	}

	_, tuiErr := tui.StartTUI(startupInfo, client, nil, mode.shutdownFunc, client.CreateLaunchTicket)
	return tuiErr
}

// resolveTUIListenURL determines the operator-reachable Web UI base URL.
// The daemon's external_url wins; otherwise uses connection URL or http://localhost:<port>.
func resolveTUIListenURL(info *model.DaemonInfo, mode tuiConnectMode) string {
	if info != nil && info.ExternalURL != "" {
		return info.ExternalURL
	}
	if mode.remote {
		return mode.connBaseURL
	}
	if info != nil && info.Port > 0 {
		return fmt.Sprintf("http://localhost:%d", info.Port)
	}
	return ""
}
