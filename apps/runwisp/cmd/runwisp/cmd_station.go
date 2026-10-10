// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
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

// loadEnvFileInto loads the RUNWISP_* variables from the .env file into the
// process environment, without overriding variables that are already set. If
// the file is missing and was explicitly requested, an error is returned; a
// missing default ".env" is silently ignored.
func loadEnvFileInto(envFile string, envFileExplicit bool) error {
	data, err := os.ReadFile(envFile)
	if errors.Is(err, fs.ErrNotExist) && !envFileExplicit {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot load env file %q: %w", envFile, err)
	}
	for key, value := range parseRunwispDotenv(string(data)) {
		if _, set := os.LookupEnv(key); !set {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseRunwispDotenv returns the RUNWISP_* assignments in a dotenv file. The
// default .env usually belongs to the surrounding project, so every other
// line, whatever its syntax, is skipped rather than rejected. Values follow
// the common dotenv forms: an optional `export ` prefix, a double-quoted value
// (with \n, \" and \\ escapes) or single-quoted value, either followed by an
// optional comment, or an unquoted value ending at a ` #` comment.
func parseRunwispDotenv(data string) map[string]string {
	out := make(map[string]string)
	for line := range strings.Lines(data) {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !strings.HasPrefix(key, "RUNWISP_") || strings.ContainsAny(key, " \t") {
			continue
		}
		out[key] = dotenvValue(strings.TrimSpace(value))
	}
	return out
}

// dotenvValue unquotes one trimmed dotenv value. An unterminated quote is
// read as an unquoted value.
func dotenvValue(v string) string {
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		var b strings.Builder
		for i := 1; i < len(v); i++ {
			c := v[i]
			if c == v[0] {
				return b.String()
			}
			if c == '\\' && v[0] == '"' && i+1 < len(v) {
				i++
				if c = v[i]; c == 'n' {
					c = '\n'
				}
			}
			b.WriteByte(c)
		}
	}
	for i := 0; i < len(v); i++ {
		if v[i] == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t') {
			v = v[:i]
			break
		}
	}
	return strings.TrimSpace(v)
}
