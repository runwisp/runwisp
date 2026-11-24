// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui"
	"golang.org/x/term"
)

// runDefault attaches the TUI to a running daemon, or runs one in this process
// with the TUI in front of it. A daemon started here lives exactly as long as
// this process: always-on is what `runwisp service install` or Docker is for.
func runDefault(ctx context.Context, f Flags) error {
	client := apiclient.NewUnix(localAPISocketPath(f))

	if client.HealthCheck(ctx) == nil {
		return runTUIConnect(ctx, client)
	}
	if err := otherUserDaemonError(f); err != nil {
		return err
	}

	serviceInstalled, err := scaffoldIfMissing(f)
	if err != nil {
		return err
	}
	if err := datadir.EnsureDir(f.DataDir); err != nil {
		return err
	}

	// When first run performed the cron cutover, systemd already owns a daemon on
	// this data dir and started it; running a second one here would fight it for
	// the port and the SQLite file. Wait for the one systemd started and attach.
	if serviceInstalled {
		if err := waitForDaemon(client, 30*time.Second, f); err != nil {
			return err
		}
		return runTUIConnect(ctx, client)
	}

	start, portErr := ensurePortFreeOrHandle(ctx, f)
	if portErr != nil || !start {
		return portErr
	}

	return runDaemon(modeStandalone, f, false)
}

// ensurePortFreeOrHandle probes the bind port before starting a daemon.
// Returns start=true when the caller should run its own daemon.
func ensurePortFreeOrHandle(ctx context.Context, f Flags) (start bool, err error) {
	if f.NoHTTP {
		return true, nil
	}
	bindErr := probePortAvailable(f.Host, f.Port)
	if bindErr == nil {
		return true, nil
	}

	info := probeRunwispInstance(f.Host, f.Port)
	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	choice, resErr := resolvePortConflict(f, bindErr, info, interactive, os.Stdin, os.Stderr)
	if resErr != nil {
		return false, resErr
	}
	switch choice {
	case conflictConnect:
		return false, runTUIConnect(ctx, apiclient.NewUnix(info.SocketPath))
	case conflictStopAndLaunch:
		if stopErr := stopDaemonByInstance(f, info); stopErr != nil {
			return false, stopErr
		}
		return true, nil // other daemon stopped; run our own
	default: // conflictAbort
		return false, nil
	}
}

// runTUIConnect launches the TUI against a local daemon via Unix socket.
// The daemon is someone else's (a service, another terminal), so quitting
// leaves it running.
func runTUIConnect(ctx context.Context, client *apiclient.Client) error {
	return launchConnectedTUI(ctx, client, tuiConnectMode{})
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
}

// launchConnectedTUI is the shared tail for local socket and remote HTTP paths.
// Pulls daemon info, resolves Web UI base URL, fills in transport-specific bits, and runs TUI.
func launchConnectedTUI(ctx context.Context, client *apiclient.Client, mode tuiConnectMode) error {
	// The TUI needs a real terminal; without one it would hang on stdin. Decline
	// clearly instead.
	if !isInteractiveTerminal() {
		return errors.New("no interactive terminal; the daemon runs headless here — use 'runwisp station' / 'runwisp daemon', or run 'runwisp tui' from a real terminal")
	}

	info, err := client.GetDaemonInfo(ctx)
	if err != nil {
		slog.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info)
	startupInfo.ListenURL = resolveTUIListenURL(info, mode)
	// Loopback/socket only: a remote attach gets an error here, which reads as
	// "not an app".
	identity, _ := client.GetInstanceInfo(ctx)
	startupInfo.AppConfig = isAppDaemon(identity)

	if !mode.remote {
		// Fetch the ephemeral password over the local socket so the TUI's
		// "Password" home field can copy it to the clipboard. The env-var case
		// (ErrLocalCredentialsUnavailable) and the RUNWISP_AUTH=off case
		// (ErrAuthDisabled) are expected, not errors — the TUI hides or replaces
		// the field based on the daemon info it already fetched. A remote
		// operator already entered the password and the endpoint is local-only,
		// so we skip it there.
		if creds, credErr := client.GetLocalCredentials(ctx); credErr == nil && creds != nil {
			startupInfo.Password = creds.Password
			startupInfo.PasswordEphemeral = creds.Ephemeral
		} else if credErr != nil &&
			!errors.Is(credErr, apiclient.ErrLocalCredentialsUnavailable) &&
			!errors.Is(credErr, apiclient.ErrAuthDisabled) {
			slog.Warn("Could not fetch local credentials", "err", credErr)
		}
	}

	// A read-only session can't mint a launch ticket (the daemon rejects the
	// POST), so the TUI hides the Open Web UI field and log download.
	var launchTicketFunc func() (string, error)
	if !client.ReadOnly() {
		launchTicketFunc = func() (string, error) {
			return client.CreateLaunchTicket(ctx)
		}
	}
	return tui.StartTUI(tui.TUIConfig{
		Info:             startupInfo,
		Client:           client,
		LaunchTicketFunc: launchTicketFunc,
		Daemon:           tui.DaemonAttached,
	}, nil)
}

// resolveTUIListenURL determines the operator-reachable Web UI base URL.
// A configured external_url wins; otherwise uses connection URL or
// http://localhost:<port>. A detected external URL is skipped: it is only the
// address of the last sign-in, possibly from another machine, and the TUI
// hands a launch ticket to whatever URL this returns.
func resolveTUIListenURL(info *model.DaemonInfo, mode tuiConnectMode) string {
	if info != nil && info.ExternalURL != "" && info.ExternalURLSource != "detected" {
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
