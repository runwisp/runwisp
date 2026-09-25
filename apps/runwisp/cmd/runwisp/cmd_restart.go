// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var restartOpts struct {
	Local bool
}

var restartRemoteFlags remoteFlags

var restartCmd = &cobra.Command{
	Use:   "restart [target...]",
	Short: "Restart the background daemon, or one or more tasks/services",
	Long: `Restart the RunWisp daemon that owns this data dir — or, given one or more
targets, restart just those without touching the daemon.

A target is a task name, a service name, or a quoted shell-style glob matched
against task and service names ('web*', or '*' for everything). Several
targets can be given at once. For a service, every instance is bounced —
starting one that was stopped (including a service that booted with
autostart=false, or one you flipped to autostart=true and reloaded). For a
task, any active run is cancelled, RunWisp waits for it to actually end, then
triggers exactly one fresh run. A target locked with manual_trigger = false is
rejected (403); a glob silently skips locked entries instead of failing.

With --url (or RUNWISP_URL), targets are restarted on a remote daemon instead
— the same CHAP login and session caching as 'runwisp run --url'.

With no target, the whole daemon restarts. Most config edits only need
'runwisp reload'; restart is for settings a reload can't apply ([daemon],
[storage], [notify], the listen address) or to re-fire run_on_start and
missed-run catch-up.

When the daemon is managed by systemd or launchd (wired up via
'runwisp service install'), the restart is delegated to the service
manager. Otherwise the daemon is stopped gracefully (SIGTERM) and a
fresh one is spawned in the background.

The delegation finds whichever unit is installed on its own. Pass --local to
pin the per-user one when both a system and a user unit are present.`,
	Example: `  runwisp restart web
  runwisp restart web worker 'batch-*'
  runwisp restart '*' --url https://ci.example.com --password "$RUNWISP_PASSWORD"
  runwisp restart`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRestart(cmd, args, flags)
	},
}

func init() {
	restartCmd.Flags().BoolVar(&restartOpts.Local, "local", false, localFlagUsage)
	addRemoteFlags(restartCmd, &restartRemoteFlags)
}

func runRestart(cmd *cobra.Command, args []string, f Flags) error {
	if len(args) > 0 {
		return controlTargets(cmd, f, args, restartControlAction, restartRemoteFlags)
	}

	if url, _ := restartRemoteFlags.resolve(); url != "" {
		return errors.New("--url needs a target; the remote daemon itself can't be restarted from here")
	}

	out := cmd.OutOrStdout()

	installer, opts, st, ok := serviceState(cmd, f, restartOpts.Local)
	if ok && shouldDelegateRestart(st) {
		return restartViaService(out, installer, opts, st, f)
	}

	if isDaemonRunning(f) {
		if err := shutdownDaemonWait(stopWaitTimeout(f), f); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(out, "No daemon was running — starting one.")
	}

	if err := spawnDaemon(f); err != nil {
		return err
	}
	client := apiclient.NewUnix(localAPISocketPath(f))
	logPath := filepath.Join(f.DataDir, "daemon.log")
	if err := waitForDaemon(client, logPath, 10*time.Second, f); err != nil {
		return err
	}
	printRestarted(out, f)
	return nil
}

// restartViaService delegates the restart to systemd/launchd, then waits for
// the fresh daemon to answer health checks before reporting success.
func restartViaService(out io.Writer, installer autostart.Installer, opts autostart.InstallOptions, st autostart.Status, f Flags) error {
	fmt.Fprintf(out, "Daemon is managed by %s — restarting %s...\n", serviceManagerName(st), filepath.Base(st.UnitPath))
	if err := installer.Restart(context.Background(), opts); err != nil {
		return err
	}
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := pollHealth(client, 15*time.Second); err != nil {
		return fmt.Errorf("daemon did not come back up after restart (%w) — check logs: %s", err, st.LogsHint)
	}
	printRestarted(out, f)
	return nil
}

// printRestarted confirms the restart and points at the web UI. The URL is
// best-effort — an unreadable config just drops the suffix.
func printRestarted(out io.Writer, f Flags) {
	if cfg, err := config.Load(f.CfgFile); err == nil {
		fmt.Fprintf(out, "Daemon restarted — web UI at %s\n", daemonListenURL(cfg, f))
		return
	}
	fmt.Fprintln(out, "Daemon restarted.")
}
