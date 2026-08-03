// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/cutover"
	"github.com/spf13/cobra"
)

// takeoverRequest is the flags `takeover` parses. Its own type rather than a
// reused installRequest: this command has no --local, no --print, and no separate
// answer to the cron question — retiring cron *is* the command.
type takeoverRequest struct {
	Yes    bool
	DryRun bool
	Force  bool
	// Binary overrides the auto-detected binary path baked into the unit.
	Binary string
	// AllowSkippedCronJobs proceeds even though some cron sources won't load.
	// Those jobs stay stopped.
	AllowSkippedCronJobs bool
}

// takeoverOpts holds the flags `takeover` parses. Package-level because cobra
// owns the singleton lifecycle.
var takeoverOpts takeoverRequest

var takeoverCmd = &cobra.Command{
	Use:   "takeover",
	Short: "Take the schedule over from cron and retire it",
	Long: `Hand your cron jobs over to RunWisp, in one step.

Run it on a box that has cron jobs and nothing else — no ` + "`runwisp.toml`" + ` needed.
RunWisp finds your crontabs, writes a config that reads them live, installs
itself as a system service so it starts on boot, then stops and masks the system
cron service so exactly one scheduler owns your jobs. Nothing is converted or
rewritten: ` + "`crontab -e`" + ` keeps working, and RunWisp re-reads the files.

Until cron is retired, RunWisp deliberately does not run the jobs it reads from
your crontabs: cron is still running them, and firing them from both would run
every one of them twice. So those tasks show up as held, and this is the command
that makes them RunWisp's.

Order matters, and nothing is destructive until RunWisp is in place: the config
and unit are written first, cron is masked second, RunWisp is started third. If
RunWisp fails to start, cron is unmasked and restarted rather than leaving the
box with no scheduler at all. ` + "`runwisp service uninstall`" + ` hands cron back.

` + "`--dry-run`" + ` prints the plan and stops, always — including the cases that cannot
proceed, so you can see what would need fixing. Re-running a finished take-over
is a no-op, so it is safe in a provisioning script.

Needs root and systemd. macOS's cron lives under SIP-protected
/System/Library/LaunchDaemons and cannot be masked by anything running as you —
there, use ` + "`runwisp import cron`" + ` and remove the crontab by hand.`,
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
	takeoverCmd.Flags().BoolVarP(&takeoverOpts.Yes, "yes", "y", false, "take over without asking (for provisioning scripts)")
	takeoverCmd.Flags().BoolVar(&takeoverOpts.AllowSkippedCronJobs, "allow-skipped-cron-jobs", false,
		"take over cron even though some cron jobs failed to load (those jobs stay stopped)")
	takeoverCmd.Flags().StringVar(&takeoverOpts.Binary, "binary", "", "override the binary path baked into the unit (default: auto-detect)")
}

// newTakeover is the seam. Resolving the real thing reaches for this host's
// systemd, its executable path and its euid; a test that wants to describe the
// command's flow substitutes the machine instead of mocking six functions.
var newTakeover = resolveCutover

// runTakeover computes one cutover plan, shows it, asks once, and performs it.
//
// Every branch below is a property of that one value: what it found, what it
// would change, and what it cannot. That is the whole point — the three commands
// that used to each re-derive whether retiring cron was legal disagreed about
// what a refusal meant, and `takeover` on a box with cron jobs but no config drew
// the shortest straw: it told the operator to go author one.
func runTakeover(cmd *cobra.Command, f Flags) error {
	out := cmd.OutOrStdout()

	c, err := newTakeover(cmd, f, takeoverOpts)
	if err != nil {
		return err
	}

	plan, err := c.Compute(cmd.Context())
	if err != nil {
		return err
	}
	cutover.Render(out, plan)

	// A blocked plan is still printed above — someone told "this needs root"
	// wants to know twelve jobs are waiting — and then exits non-zero, so a
	// provisioning script cannot mistake it for a take-over that happened.
	if plan.Blocked() {
		return blockedCutoverError(plan)
	}

	if plan.NothingToDo() {
		fmt.Fprintln(out, "\nNothing to do — RunWisp already owns the cron jobs on this box.")
		return nil
	}

	if takeoverOpts.DryRun {
		fmt.Fprintln(out, "\nDry run — nothing was written, and cron is untouched.")
		return nil
	}

	ok, err := c.Confirm(plan)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(out, "Aborted — nothing was written, and cron is untouched.")
		return nil
	}

	res, err := c.Execute(cmd.Context(), plan, out)
	if err != nil {
		return asUserFacing(err)
	}
	if res.SettingsStale {
		fmt.Fprintln(out, staleSettingsNote)
	}
	return nil
}
