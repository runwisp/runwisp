// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui"
	"github.com/spf13/cobra"
)

var stationFlags struct {
	Token   string
	URL     string
	EnvFile string
}

var stationCmd = &cobra.Command{
	Use:   "station",
	Short: "Start in station mode",
	Long: `Starts the daemon in station mode, connecting to a RunWisp Station control plane.
Requires RUNWISP_STATION_TOKEN to be set (via environment or .env file).
The local scheduler is not started — task scheduling is managed by the Station.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := resolveStationEnv(stationFlags.EnvFile, cmd.Flags().Changed("env-file"), stationFlags.Token, stationFlags.URL); err != nil {
			return err
		}
		if noTUI || !isInteractiveTerminal() {
			return runDaemon(modeStation, flags, noTUI)
		}
		return runStationInteractive(cmd.Context(), flags)
	},
}

// runStationInteractive boots station mode the same way the bare `runwisp` command
// boots standalone mode: spawn the daemon as a detached background process and
// attach the TUI to it over the local socket. Keeping the daemon in its own
// process is what protects the operator's terminal — a daemon crash can never
// leave the attached TUI's terminal in raw/alt-screen mode, because the TUI
// process (which owns the terminal) is not the one that died.
func runStationInteractive(ctx context.Context, f Flags) error {
	client := apiclient.NewUnix(localAPISocketPath(f))

	// A station daemon is already running on this data dir — just attach.
	if client.HealthCheck(ctx) == nil {
		return runTUIConnect(ctx, client, f, tui.DaemonAttached)
	}
	return spawnAndAttach(ctx, client, f, modeStation)
}

// resolveStationEnv loads the .env file (if present) and applies the --token /
// --url overrides into the process environment, then requires a station token.
// Shared by the `station` command and `demo --station`.
func resolveStationEnv(envFile string, envFileExplicit bool, token, url string) error {
	if err := loadEnvFileInto(envFile, envFileExplicit); err != nil {
		return err
	}
	if token != "" {
		os.Setenv("RUNWISP_STATION_TOKEN", token)
	}
	if url != "" {
		os.Setenv("RUNWISP_STATION_URL", url)
	}
	if os.Getenv("RUNWISP_STATION_TOKEN") == "" {
		return errors.New("RUNWISP_STATION_TOKEN is required — set it via environment, .env file, or --token flag")
	}
	return nil
}

func init() {
	stationCmd.Flags().StringVar(&stationFlags.Token, "token", "", "station token (overrides RUNWISP_STATION_TOKEN)")
	stationCmd.Flags().StringVar(&stationFlags.URL, "url", "", "station API URL (overrides RUNWISP_STATION_URL)")
	stationCmd.Flags().StringVar(&stationFlags.EnvFile, "env-file", ".env", "path to .env file for station configuration")
	stationCmd.Flags().BoolVar(&noTUI, "no-tui", false, "run in headless daemon mode (no interactive TUI)")
}

// loadEnvFileInto loads variables from the .env file into the process
// environment, without overriding variables that are already set. Values are
// literal, like task env_file. If the file is missing and was explicitly
// requested, an error is returned; a missing default ".env" is silently ignored.
func loadEnvFileInto(envFile string, envFileExplicit bool) error {
	values, err := config.ReadEnvFile(envFile)
	if errors.Is(err, fs.ErrNotExist) && !envFileExplicit {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot load env file %q: %w", envFile, err)
	}
	for key, value := range values {
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}
