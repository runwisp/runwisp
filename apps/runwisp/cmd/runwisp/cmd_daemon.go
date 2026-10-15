// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
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
		return runDaemon(modeStandalone, f, true)
	},
}

var daemonApp bool

func init() {
	daemonCmd.Flags().BoolVar(&daemonApp, "app", false, appFlagUsage)
}
