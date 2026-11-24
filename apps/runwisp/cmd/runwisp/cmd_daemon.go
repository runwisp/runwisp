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
	Short: "Run RunWisp in the foreground without the TUI",
	Long: `Runs the scheduler, HTTP API, and web UI in the foreground without the
interactive terminal interface, until Ctrl+C or SIGTERM. This is what a
service manager or a Docker container runs. For the TUI, run 'runwisp' with no
subcommand; quitting it stops RunWisp too. To keep RunWisp running after you
log out, install it with 'runwisp service install'.`,
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
		f.NoHTTP = daemonNoHTTP
		return runDaemon(modeStandalone, f, true)
	},
}

var (
	daemonApp    bool
	daemonNoHTTP bool
)

func init() {
	daemonCmd.Flags().BoolVar(&daemonApp, "app", false, appFlagUsage)
	daemonCmd.Flags().BoolVar(&daemonNoHTTP, "no-http", false, "don't open the HTTP port (no Web UI or remote API); the TUI and CLI still work over the control socket")
}
