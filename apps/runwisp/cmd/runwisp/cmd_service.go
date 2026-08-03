// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"github.com/spf13/cobra"
)

// serviceCmd is the parent group for autostart wiring. Child commands
// hook into systemd (Linux/WSL) or launchd (macOS) so the daemon
// survives a reboot. `runwisp status` answers "is the daemon alive
// right now?"; `runwisp service status` answers "is autostart wired
// up?".
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage RunWisp autostart (systemd on Linux/WSL, launchd on macOS)",
	Long: `Manage RunWisp autostart so the daemon survives a reboot.

  runwisp service install     wire up systemd or launchd to start on boot
  runwisp service uninstall   remove the unit/plist (does not touch data)
  runwisp service status      show autostart and binary/unit drift

This is OS integration only — it never edits runwisp.toml.

For "is the daemon alive right now?" use 'runwisp status' instead.`,
	// Service commands describe a future data dir (baked into the unit) —
	// they do not write to it, so this replaces the root PersistentPreRunE
	// rather than inheriting it: that one would mkdir paths the operator
	// has not consented to yet. Cobra runs only the nearest hook, so
	// everything else the root one does has to be repeated here — in
	// particular resolvePathDefaults, without which --config/--data left
	// unset stay empty strings and a system install bakes an empty path
	// into the unit.
	PersistentPreRunE: servicePathPreRun,
}

// servicePathPreRun is the pre-run for commands that describe a data dir they are
// about to bake into a unit rather than one they write to now. Shared with
// `runwisp takeover`, which installs the same unit from outside this subtree.
func servicePathPreRun(cmd *cobra.Command, _ []string) error {
	if err := resolveLogConfig(); err != nil {
		return err
	}
	// The port is baked into the unit, so the RUNWISP_PORT fallback has to
	// apply here too: without it `RUNWISP_PORT=8080 sudo runwisp takeover`
	// writes a unit pinned to a port the operator never asked for, and the
	// daemon it starts binds somewhere they aren't looking.
	if err := resolvePortConfig(cmd); err != nil {
		return err
	}
	resolvePathDefaults(os.Geteuid())
	return nil
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
}
