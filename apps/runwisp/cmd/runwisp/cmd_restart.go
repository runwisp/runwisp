// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
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
	System bool
}

var restartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the background daemon (applies runwisp.toml changes)",
	Long: `Restart the RunWisp daemon that owns this data dir.

A running daemon keeps the task set it loaded at boot — editing
runwisp.toml has no effect until a restart. This command is how you
apply config changes.

When the daemon is managed by systemd or launchd (wired up via
'runwisp service install'), the restart is delegated to the service
manager. Otherwise the daemon is stopped gracefully (SIGTERM) and a
fresh one is spawned in the background.

Pass --system if the daemon was installed with 'service install --system',
so the service-managed delegation checks the right unit.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runRestart(cmd, flags)
	},
}

func init() {
	restartCmd.Flags().BoolVar(&restartOpts.System, "system", false, "the daemon was installed system-wide (Linux, advanced)")
}

func runRestart(cmd *cobra.Command, f Flags) error {
	out := cmd.OutOrStdout()

	installer, opts, st, ok := serviceState(cmd, f, restartOpts.System)
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
