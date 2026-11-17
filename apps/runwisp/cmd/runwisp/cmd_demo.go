// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/demo"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

var demoFlags struct {
	Station  bool
	NoTUI    bool
	Token    string
	URL      string
	EnvFile  string
	SeedOnly bool
}

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Boot a throwaway, fully-populated RunWisp instance",
	Long: `Boots RunWisp against a temporary config and data directory so you can
explore the TUI and Web UI without writing a runwisp.toml.

The demo loads a realistic "Acme Notes" config (scheduled backups and health
checks, background services, manual deploy/import tasks) and — in standalone
mode — pre-seeds hundreds of historical runs with real captured logs, timed to
match each task's cron schedule. Quitting the TUI shuts the demo down.
Everything lives under a temp directory that is deleted when it stops.

With --station the daemon connects to the control plane instead and no history is
seeded (the Station owns it); RUNWISP_STATION_TOKEN is required, as for ` + "`runwisp station`" + `.

With --no-tui the demo runs in the foreground without the TUI, handy over SSH
when you only want the Web UI. Ctrl+C stops it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDemo(cmd, flags)
	},
}

func init() {
	demoCmd.Flags().BoolVar(&demoFlags.Station, "station", false, "connect to the station control plane instead of seeding local history")
	demoCmd.Flags().BoolVar(&demoFlags.NoTUI, "no-tui", false, "run in the foreground without the TUI (Ctrl+C stops it)")
	demoCmd.Flags().StringVar(&demoFlags.Token, "token", "", "station token, with --station (overrides RUNWISP_STATION_TOKEN)")
	demoCmd.Flags().StringVar(&demoFlags.URL, "url", "", "station API URL, with --station (overrides RUNWISP_STATION_URL)")
	demoCmd.Flags().StringVar(&demoFlags.EnvFile, "env-file", ".env", "path to .env file, with --station")
	demoCmd.Flags().BoolVar(&demoFlags.SeedOnly, "seed-only", false, "write the demo config to --config and seed --data, then exit without starting a daemon or TUI (for tooling that boots its own daemon)")
}

// demoPathFlagsRejection reports the error the full demo returns when the
// operator passed a --config/--data it can't honor: the demo runs against a
// throwaway temp dir, so honoring either would be a lie. Both unset → nil, and
// the demo proceeds. --seed-only takes a different path (it writes to the given
// paths) and never reaches this check.
func demoPathFlagsRejection(configChanged, dataChanged bool) error {
	if !configChanged && !dataChanged {
		return nil
	}
	return fmt.Errorf("demo runs against a throwaway temp directory and cannot honor --config/--data; " +
		"use `runwisp demo --seed-only --config <path> --data <path>` to seed your own paths instead")
}

// demoTerminalRejection reports the error the full demo returns when it has no
// terminal for its TUI, rather than silently running headless. Checked before
// the temp dir and seeding, so nothing is left behind. --no-tui is the headless
// path and never needs a terminal.
func demoTerminalRejection(noTUI, interactive bool) error {
	if noTUI || interactive {
		return nil
	}
	return errors.New("demo needs an interactive terminal for its TUI; " +
		"use `runwisp demo --no-tui` to run it without the TUI")
}

// runDemo sets up a throwaway temp dir, writes the embedded demo config, seeds
// history (standalone only), and runs the daemon against the temp dir in this
// process, deleting the dir once it stops.
func runDemo(cmd *cobra.Command, f Flags) error {
	// --seed-only writes the demo config and seeds history into the
	// caller-supplied --config/--data, then exits. No temp dir, no daemon, no
	// TUI: the caller (e.g. the docs screenshot harness) boots its own daemon
	// against those paths. Station mode owns its history, so the two are mutually
	// exclusive.
	if demoFlags.SeedOnly {
		if demoFlags.Station {
			return errors.New("demo: --seed-only cannot be combined with --station")
		}
		return setupDemoDir(cmd, f)
	}

	// The full demo runs against a throwaway temp dir it deletes on shutdown, so
	// an explicit --config/--data can't be honored. Reject them and point at
	// --seed-only, which does write to the supplied paths.
	if err := demoPathFlagsRejection(cmd.Flags().Changed("config"), cmd.Flags().Changed("data")); err != nil {
		return err
	}
	if err := demoTerminalRejection(demoFlags.NoTUI, isInteractiveTerminal()); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "runwisp-demo-")
	if err != nil {
		return fmt.Errorf("create demo temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Redirect every path the daemon and TUI resolve at the throwaway dir.
	// These mutate a local copy of Flags, never the package global, so demo
	// runs stay isolated and tests don't have to snapshot/restore state.
	f.CfgFile = filepath.Join(tmp, "runwisp.toml")
	f.DataDir = filepath.Join(tmp, "data")

	if err := setupDemoDir(cmd, f); err != nil {
		return err
	}
	if err := demoResolvePort(cmd, &f); err != nil {
		return err
	}

	mode := modeStandalone
	if demoFlags.Station {
		mode = modeStation
	}
	return runDaemon(mode, f, demoFlags.NoTUI)
}

// demoResolvePort fails fast on a port conflict before spawning a daemon that
// can't bind. A throwaway demo on the default port just moves to a free one; an
// explicit --port is still honored or rejected.
func demoResolvePort(cmd *cobra.Command, f *Flags) error {
	bindErr := probePortAvailable(f.Host, f.Port)
	if bindErr == nil {
		return nil
	}
	if cmd.Flags().Changed("port") {
		return portConflictMessage(f.Host, f.Port, bindErr, probeRunwispInstance(f.Host, f.Port))
	}
	port, err := freePort(f.Host)
	if err != nil {
		return fmt.Errorf("find a free port: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Port %d is taken, using %d instead.\n", f.Port, port)
	f.Port = port
	return nil
}

// setupDemoDir writes the embedded config, creates the data/log dirs, and either
// resolves station credentials (--station) or seeds the fake run history.
func setupDemoDir(cmd *cobra.Command, f Flags) error {
	// --seed-only backs the screenshot/video tooling, which wants the demo
	// config's fictional external_url to show a believable operator domain.
	// The interactive demo wants "Open Web UI" to reach the real throwaway
	// daemon instead, so it drops external_url.
	if err := demo.WriteConfig(f.CfgFile, demoFlags.SeedOnly); err != nil {
		return err
	}
	if err := datadir.EnsureDir(f.DataDir); err != nil {
		return err
	}
	if err := datadir.EnsureDir(f.LogDir()); err != nil {
		return err
	}

	if demoFlags.Station {
		return resolveStationEnv(demoFlags.EnvFile, cmd.Flags().Changed("env-file"), demoFlags.Token, demoFlags.URL)
	}
	return seedDemoHistory(f)
}

// seedDemoHistory loads the just-written demo config and populates the database
// and log directory with believable historical runs.
func seedDemoHistory(f Flags) error {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return fmt.Errorf("load demo config: %w", err)
	}
	db, err := storage.New(f.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintln(os.Stderr, "Seeding demo history by running your tasks (a few seconds)...")
	n, err := demo.Seed(context.Background(), db, cfg, f.LogDir(), time.Now())
	if err != nil {
		return fmt.Errorf("seed demo data: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Seeded %d demo runs.\n", n)
	return nil
}
