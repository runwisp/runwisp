// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/tui"
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
			return fmt.Errorf(
				"another RunWisp daemon is already running on port %d with a different password\n\n"+
					"This usually happens when an instance was started from a different directory.\n"+
					"To resolve this, you can:\n"+
					"  - Stop the other daemon first\n"+
					"  - Use a different port:  runwisp --port <PORT>\n"+
					"  - Set RUNWISP_PASSWORD to the other daemon's password to connect to it",
				flags.Port,
			)
		}
		return err
	}

	if err := spawnDaemon(); err != nil {
		log.Warn("Failed to spawn background daemon, running inline", "err", err)
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
		log.Warn("Could not fetch daemon info", "err", err)
	}

	startupInfo := buildStartupInfoFromDaemon(info, password, passwordExplicit)

	_, tuiErr := tui.StartTUI(startupInfo, client, nil, shutdownDaemon, client.CreateLaunchTicket)
	if tuiErr != nil {
		return tuiErr
	}

	return nil
}
