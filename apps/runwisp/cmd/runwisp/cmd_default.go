// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	spawn, portErr := ensurePortFreeOrHandle(f)
	if portErr != nil || !spawn {
		return portErr
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

// ensurePortFreeOrHandle probes the bind port before a spawn. It returns
// spawn=true when the caller should go on to launch its own daemon (port free,
// or a conflicting daemon was stopped). When a conflicting RunWisp instance is
// found it resolves the conflict interactively: connecting to it (running the
// TUI inline and returning spawn=false) or aborting (spawn=false, nil error).
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

// runTUIConnect launches the TUI against an already-healthy LOCAL daemon
// reached over its Unix socket. Local access is gated by data-dir filesystem
// permissions, so there is no Authenticate step; the daemon's ephemeral
// password is fetched only so the Home screen can offer it for clipboard copy,
// and the quit dialog can shut the daemon down via its local PID/socket.
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

// launchConnectedTUI is the shared tail for both the socket and the remote HTTP
// paths: it pulls daemon info, resolves the Web UI base URL, fills in the
// transport-specific bits, and runs the Bubble Tea program until the user quits.
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

// resolveTUIListenURL determines the operator-reachable Web UI base URL the TUI
// uses for its "Open Web UI" and copy-URL actions. The daemon's external_url
// wins when set (it is the canonical public address, e.g. behind a proxy);
// otherwise a remote TUI uses the URL it connected to, and a local TUI uses
// http://localhost:<port>.
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
