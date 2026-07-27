// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
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
	// Service commands describe a future data dir (baked into the
	// unit) — they do not write to it. Skip the root PersistentPreRunE
	// that would otherwise mkdir paths the user has not consented to.
	PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
}

func init() {
	serviceCmd.AddCommand(serviceInstallCmd)
	serviceCmd.AddCommand(serviceUninstallCmd)
	serviceCmd.AddCommand(serviceStatusCmd)
}
