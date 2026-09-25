// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
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
	Use:   "stop [target...]",
	Short: "Stop the background daemon, or one or more tasks/services",
	Long: `Stop the RunWisp daemon that owns this data dir — or, given one or more
targets, stop just those without touching the daemon.

A target is a task name, a service name, a run ID, or a quoted shell-style
glob matched against task and service names ('web*', or '*' for everything).
Several targets can be given at once. For a service, every live instance is
cancelled and its slots stop refilling until a 'runwisp start'/'restart' or a
daemon restart. For a task, any active run is cancelled and anything still
queued is dropped — the cron schedule keeps firing. A run ID stops just that
run, wherever it came from. A target locked with manual_trigger = false is
rejected (403); a glob silently skips locked entries instead of failing.

With --url (or RUNWISP_URL), targets are stopped on a remote daemon instead —
the same CHAP login and session caching as 'runwisp run --url'.

With no target, the whole daemon stops. When it is managed by systemd or
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
	Example: `  runwisp stop web
  runwisp stop web worker 'batch-*'
  runwisp stop 01J8Z3K9QK6VN8XG2R5F7T1C4M
  runwisp stop '*' --url https://ci.example.com --password "$RUNWISP_PASSWORD"
  runwisp stop`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStop(cmd, args, flags)
	},
}

func init() {
	stopCmd.Flags().BoolVar(&stopOpts.Local, "local", false, localFlagUsage)
	addRemoteFlags(stopCmd)
}

func runStop(cmd *cobra.Command, args []string, f Flags) error {
	if len(args) > 0 {
		return controlTargets(cmd, f, controlRemote, args, "stop", "stopped", (*apiclient.Client).StopTask, (*apiclient.Client).StopRun)
	}

	if url, _ := controlRemote.resolve(); url != "" {
		return errors.New("--url needs a target; the remote daemon itself can't be stopped from here")
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
