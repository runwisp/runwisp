// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// takeoverOpts holds the flags `takeover` parses. Same lifecycle reasoning as
// serviceInstallOpts: cobra owns the singleton.
var takeoverOpts installRequest

var takeoverCmd = &cobra.Command{
	Use:   "takeover",
	Short: "Take the schedule over from cron and retire it",
	Long: `Hand your cron jobs over to RunWisp, in one step.

This installs the system-wide service (so RunWisp starts on boot), then stops
and masks the system cron service, so exactly one scheduler owns your jobs. It
is ` + "`service install --take-over-cron`" + ` with the reload afterwards, and it is
what the "jobs are held" warning points you at.

Until cron is retired, RunWisp deliberately does not run the jobs it reads from
your crontabs: cron is still running them, and firing them from both would run
every one of them twice. So those tasks show up as held, and this is the command
that makes them RunWisp's.

Order matters, and nothing is destructive until RunWisp is in place: the unit is
written first, cron is masked second, RunWisp is started third. If RunWisp fails
to start, cron is unmasked and restarted rather than leaving the box with no
scheduler at all. ` + "`runwisp service uninstall`" + ` hands cron back.

Needs root and systemd. macOS's cron lives under SIP-protected
/System/Library/LaunchDaemons and cannot be masked by anything running as you —
there, use ` + "`runwisp import cron`" + ` and remove the crontab by hand.

Flags:
  --dry-run    print the plan and exit without writing anything
  --force      overwrite a hand-edited unit
  --allow-skipped-cron-jobs  proceed even though some cron jobs failed to load`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runTakeover(cmd, flags)
	},
	// Like the service commands, this describes a data dir it is about to bake
	// into a unit rather than one it writes to now, so it replaces the root hook
	// instead of inheriting its EnsureDir. resolvePathDefaults still has to run
	// or an unset --config/--data bakes an empty path into the unit.
	PersistentPreRunE: servicePathPreRun,
}

func init() {
	takeoverCmd.Flags().BoolVar(&takeoverOpts.DryRun, "dry-run", false, "print the plan and exit without writing")
	takeoverCmd.Flags().BoolVar(&takeoverOpts.Force, "force", false, "overwrite a hand-edited unit")
	takeoverCmd.Flags().BoolVarP(&takeoverOpts.Yes, "yes", "y", false, "skip the install confirmation")
	takeoverCmd.Flags().BoolVar(&takeoverOpts.AllowSkippedCronJobs, "allow-skipped-cron-jobs", false,
		"take over cron even though some cron jobs failed to load (those jobs stay stopped)")
	takeoverCmd.Flags().StringVar(&takeoverOpts.Binary, "binary", "", "override the binary path baked into the unit (default: auto-detect)")
}

// runTakeover installs the service with the cron take-over already answered yes,
// then reloads whatever daemon is running.
//
// The reload is the part `service install --take-over-cron` cannot do for an
// operator who is already running RunWisp: masking cron does not change the
// running daemon's view of the world, and `systemctl enable --now` on an already
// active unit is a no-op. Without the reload the jobs would stay held until the
// operator worked out that they had to reload — which is exactly the "you have to
// already know" problem this command exists to remove.
// Seams. The install path reaches straight for the real systemd/launchd of the
// host it is running on, and a reload talks to a real socket; a test that wants
// to describe the ordering between them substitutes these instead of the
// machine.
var (
	takeoverInstall = installService
	takeoverReload  = runReload
)

func runTakeover(cmd *cobra.Command, f Flags) error {
	req := takeoverOpts
	// Unlike `service install`, where this is opt-in, saying yes to cron is the
	// entire point here — so a refusal is a hard error rather than a silent
	// downgrade to an install that leaves cron running.
	req.TakeOverCron = true

	installed, err := takeoverInstall(cmd, f, req)
	if err != nil || !installed {
		return err
	}

	// A daemon that was not running before the install is the one systemd just
	// started, and it loaded its config after cron was masked — nothing is held,
	// so there is nothing to reload for.
	if !isDaemonRunning(f) {
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Reloading the running daemon so the held jobs become RunWisp's...")
	return takeoverReload(cmd, f)
}
