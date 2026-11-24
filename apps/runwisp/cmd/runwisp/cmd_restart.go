// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/spf13/cobra"
)

// restartLocal backs --local.
var restartLocal bool

var restartCmd = &cobra.Command{
	Use:   "restart [target...]",
	Short: "Restart the daemon, or one or more tasks/services",
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

With --attach, it then follows the logs of the targets it acted on, as
'runwisp logs -f' does: each run it starts is shown from its first line.

With --url (or RUNWISP_URL), targets are restarted on a remote daemon instead
— the same CHAP login and session caching as 'runwisp run --url'.

With --token (or RUNWISP_HOOK_TOKEN) and --url, it uses one of the unit's
hook_tokens instead of a password, so a task that runs as another user can
still restart a service. A token takes literal names only: no globs, run IDs
or --attach.

With no target, the whole daemon restarts. Most config edits only need
'runwisp reload'; restart is for settings a reload can't apply ([daemon],
[storage], [notify], the listen address) or to re-fire run_on_start and
missed-run catch-up.

A daemon installed with 'runwisp service install' restarts through systemd or
launchd, which finds whichever unit is installed. Pass --local to pin the
per-user one when both a system and a user unit are present. Any other daemon
('runwisp', 'runwisp daemon', a Docker container) restarts in place: it shuts
down gracefully and starts again in the same process, so it keeps its terminal
or container.`,
	Example: `  runwisp restart web
  runwisp restart web worker 'batch-*'
  runwisp restart --attach 'web*'
  runwisp restart '*' --url https://ci.example.com --password "$RUNWISP_PASSWORD"
  runwisp restart shop --url http://127.0.0.1:9477 --token "$SHOP_HOOK_TOKEN"
  runwisp restart`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRestart(cmd, args, flags, controlAttach)
	},
}

func init() {
	restartCmd.Flags().BoolVar(&restartLocal, "local", false, localFlagUsage)
	addRemoteFlags(restartCmd)
	addTokenFlag(restartCmd)
	addAttachFlag(restartCmd)
}

func runRestart(cmd *cobra.Command, args []string, f Flags, attach bool) error {
	if len(args) > 0 {
		return controlTargets(cmd, f, controlRemote, args, restartVerb, attach)
	}
	if attach {
		return errAttachNeedsTarget
	}

	if url, _ := controlRemote.resolve(); url != "" {
		return errors.New("--url needs a target; the remote daemon itself can't be restarted from here")
	}
	if controlRemote.hookToken() != "" {
		return errors.New("--token needs a target; a hook token can't restart the daemon itself")
	}

	return restartDaemon(cmd, f, restartLocal)
}

// restartDaemon restarts the local daemon: through systemd/launchd when a unit
// manages it, otherwise in place, under the same PID, so the terminal or
// container it runs in keeps it. local pins the per-user unit (restart --local).
func restartDaemon(cmd *cobra.Command, f Flags, local bool) error {
	out := cmd.OutOrStdout()

	if err := otherUserDaemonError(f); err != nil {
		return err
	}
	// Pin the dir the running daemon was found in, so the post-restart health
	// check polls it and not the default (an empty ./.runwisp next to an app's dir).
	f.DataDir = clientDataDir(f)
	running := isDaemonRunning(f)
	info, err := runningDaemonInfo(cmd, f, running)
	if err != nil {
		return err
	}

	installer, opts, st, ok := serviceState(cmd, f, local)
	delegate := ok && shouldDelegateRestart(st)
	if !delegate && !running {
		return &userFacingError{
			title:   "no daemon is running here",
			details: "Start one with `runwisp` (with the TUI) or `runwisp daemon`, or install it as a service with `runwisp service install`.",
		}
	}
	// The daemon comes back with the --config it runs with, which it reports.
	cfgPath := runningConfigPath(info, f)
	if err := validateRestartConfig(cfgPath); err != nil {
		return err
	}
	if delegate {
		return restartViaService(out, installer, opts, st, f)
	}
	return restartInPlace(cmd, f, cfgPath)
}

// restartInPlace sends the daemon restartSignal: it shuts down gracefully and
// re-executes itself under the same PID. It returns once the fresh daemon
// answers, which it tells apart from the old one by its start time.
func restartInPlace(cmd *cobra.Command, f Flags, cfgPath string) error {
	ctx, out := cmd.Context(), cmd.OutOrStdout()
	client := apiclient.NewUnix(localAPISocketPath(f))
	before, err := client.GetDaemonInfo(ctx)
	if err != nil {
		return fmt.Errorf("cannot reach the daemon: %w", err)
	}
	pid, err := datadir.ReadPidFile(f.DataDir)
	if err != nil {
		return fmt.Errorf("cannot read daemon PID file: %w", err)
	}
	if err := syscall.Kill(pid, restartSignal); err != nil {
		return fmt.Errorf("cannot signal daemon process %d: %w", pid, err)
	}
	fmt.Fprintf(out, "Restarting the daemon (pid %d)...\n", pid)

	timeout := stopWaitTimeout(cfgPath) + 15*time.Second
	restarted := false
	pollUntil(time.Now().Add(timeout), func() bool {
		if !datadir.ProcessAlive(pid) {
			return true
		}
		info, err := client.GetDaemonInfo(ctx)
		restarted = err == nil && info.StartedAt.After(before.StartedAt)
		return restarted
	})
	switch {
	case restarted:
		printRestarted(out, f)
		return nil
	case !datadir.ProcessAlive(pid):
		return errors.New("the daemon stopped instead of starting again; check its output for the reason")
	default:
		return fmt.Errorf("the daemon did not come back within %s; check its output", timeout)
	}
}

// runningDaemonInfo asks the running daemon about itself, and refuses when it
// belongs to an SDK app, which restarts it on its own. It is nil when no
// daemon is running or none answered.
func runningDaemonInfo(cmd *cobra.Command, f Flags, running bool) (*model.InstanceInfo, error) {
	if !running {
		return nil, nil
	}
	info, err := apiclient.NewUnix(localAPISocketPath(f)).GetInstanceInfo(cmd.Context())
	if err != nil {
		return nil, nil // a daemon that does not answer is just unknown
	}
	return info, appDaemonRestartError(info)
}

// runningConfigPath is the config the running daemon reported, or f.CfgFile
// when none answered.
func runningConfigPath(info *model.InstanceInfo, f Flags) string {
	if info != nil && info.ConfigPath != "" {
		return info.ConfigPath
	}
	return f.CfgFile
}

// validateRestartConfig loads the config the restarted daemon will read, so a
// broken file is reported while the old daemon is still up. Reload is
// validate-first; restart has to be too, or the fresh daemon exits on the same
// error and takes every task and service down with it.
func validateRestartConfig(path string) error {
	if _, err := config.Load(path); err != nil {
		return &userFacingError{
			title:   "the config is invalid, so the daemon was left running",
			details: err.Error() + "\n\nFix it, check it with `runwisp validate`, then restart again.",
		}
	}
	return nil
}

// appDaemonRestartError refuses to restart a daemon an app started: it would
// come back reading runwisp.toml instead of the app's config. nil for any
// other daemon.
func appDaemonRestartError(info *model.InstanceInfo) error {
	if !isAppDaemon(info) {
		return nil
	}
	return &userFacingError{
		title:   "this daemon runs an app's tasks, so `runwisp restart` can't bring it back",
		details: "Restart the app instead: it starts RunWisp with its config. `runwisp stop` stops the daemon.",
	}
}

// connectedAppDaemon reports whether the local daemon answering on the control
// socket gets its config from an app. A missing or slow daemon is "no".
func connectedAppDaemon(ctx context.Context, f Flags) bool {
	ctx, cancel := context.WithTimeout(ctx, liveStateTimeout)
	defer cancel()
	info, err := apiclient.NewUnix(localAPISocketPath(f)).GetInstanceInfo(ctx)
	return err == nil && isAppDaemon(info)
}

// isAppDaemon reports whether an SDK app started the daemon, with its own
// config: only the app can restart it.
func isAppDaemon(info *model.InstanceInfo) bool {
	return info != nil && info.ConfigSource == "app"
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

// printRestarted confirms the restart and points at the web UI, on the port
// the daemon reports (it may differ from this command's --port). The URL is
// best-effort — an unreadable config just drops the suffix, and so does a
// daemon reporting port 0 (`daemon --no-http`, no web UI to point at).
func printRestarted(out io.Writer, f Flags) {
	if info, err := apiclient.NewUnix(localAPISocketPath(f)).GetDaemonInfo(context.Background()); err == nil {
		if info.Port == 0 {
			fmt.Fprintln(out, "Daemon restarted.")
			return
		}
		f.Port = info.Port
	}
	if cfg, err := config.Load(f.CfgFile); err == nil {
		fmt.Fprintf(out, "Daemon restarted — web UI at %s\n", daemonListenURL(cfg, f))
		return
	}
	fmt.Fprintln(out, "Daemon restarted.")
}
