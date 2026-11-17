// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/spf13/cobra"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Start RunWisp in headless mode (no TUI)",
	Long:  `Starts the scheduler, HTTP API, and web UI without the interactive terminal interface. Ideal for Docker containers and background services. For an attached TUI, run 'runwisp' with no subcommand.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f, err := withAppDocument(flags, cmd.InOrStdin(), daemonApp)
		if err != nil {
			return err
		}
		if daemonApp {
			// An SDK app's data dir sits in its project (storage/runwisp, .runwisp).
			if err := datadir.IgnoreInGit(f.DataDir); err != nil {
				slog.Warn("Could not write .gitignore in the data dir", "dir", f.DataDir, "err", err)
			}
		}
		return runDaemon(modeStandalone, f, true)
	},
}

var daemonApp bool

func init() {
	daemonCmd.Flags().BoolVar(&daemonApp, "app", false, appFlagUsage)
}
