// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/spf13/cobra"
)

var stopOpts struct {
	Local bool
}

var stopCmd = &cobra.Command{
	Use:   "stop [service]",
	Short: "Stop the background daemon, or a single service",
	Long: `Stop the RunWisp daemon that owns this data dir — or, given a service
name, stop just that service without touching the daemon.

With a service name ('runwisp stop web'), the daemon cancels every live
instance of that service and stops refilling its slots until a
'runwisp restart web' or a daemon restart. Only services can be stopped
this way; a scheduled task is triggered with 'runwisp run', not stopped.

With no argument, the whole daemon stops. When it is managed by systemd or
launchd (wired up via 'runwisp service install'), the stop is delegated to
the service manager so its view of the unit stays in sync — the unit remains
installed and enabled, and the daemon will come back on the next boot or
'runwisp restart'.

Otherwise the daemon receives SIGTERM and we wait for a graceful exit.
In-flight runs get [daemon] shutdown_timeout to finish; anything still
running after that is recorded with a terminal status — nothing is lost
silently.

The delegation finds whichever unit is installed on its own. Pass --local to
pin the per-user one when both a system and a user unit are present.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStop(cmd, args, flags)
	},
}

func init() {
	stopCmd.Flags().BoolVar(&stopOpts.Local, "local", false, localFlagUsage)
}

func runStop(cmd *cobra.Command, args []string, f Flags) error {
	if len(args) == 1 {
		return controlService(cmd, f, args[0], "stop", "stopped", (*apiclient.Client).StopService)
	}

	out := cmd.OutOrStdout()

	installer, opts, st, ok := serviceState(cmd, f, stopOpts.Local)
	if ok && shouldDelegateStop(st) {
		return stopViaService(out, installer, opts, st, f)
	}

	if !isDaemonRunning(f) {
		fmt.Fprintf(out, "No daemon is running on data dir %s — nothing to stop.\n", absPathOrFallback(f.DataDir))
		return nil
	}

	if err := shutdownDaemonWait(stopWaitTimeout(f), f); err != nil {
		return err
	}
	fmt.Fprintln(out, "Daemon stopped.")
	return nil
}

// stopViaService delegates the stop to systemd/launchd and waits for the
// process to actually exit (launchctl kill returns before the job dies).
func stopViaService(out io.Writer, installer autostart.Installer, opts autostart.InstallOptions, st autostart.Status, f Flags) error {
	fmt.Fprintf(out, "Daemon is managed by %s — stopping %s...\n", serviceManagerName(st), filepath.Base(st.UnitPath))
	if err := installer.Stop(context.Background(), opts); err != nil {
		return err
	}
	if pid, err := datadir.ReadPidFile(f.DataDir); err == nil {
		if err := waitForProcessExit(pid, stopWaitTimeout(f), f.DataDir); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "Daemon stopped. It stays enabled and will start again on the next boot; 'runwisp service uninstall' removes it for good.")
	return nil
}
