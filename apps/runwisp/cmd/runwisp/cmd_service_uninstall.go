// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/spf13/cobra"
)

var serviceUninstallOpts struct {
	Yes   bool
	Purge bool
	Force bool
	Local bool
}

var serviceUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove the autostart unit/plist (data dir is preserved)",
	Long: `Symmetric counterpart of 'runwisp service install': stops the service,
disables autostart, and deletes the managed unit/plist. The data dir is
preserved by default.

With no flags this finds whichever unit is installed — the system-wide
service or a per-user one. Pass --local to pin the per-user unit when both
are present. If the install took over cron, this hands it back.

Pass --purge to also remove the data dir. --purge requires typing the
literal word 'delete' to confirm; --yes does NOT skip that prompt.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServiceUninstall(cmd, flags)
	},
}

func init() {
	serviceUninstallCmd.Flags().BoolVarP(&serviceUninstallOpts.Yes, "yes", "y", false, "skip confirmation prompt (does not skip --purge literal word)")
	serviceUninstallCmd.Flags().BoolVar(&serviceUninstallOpts.Purge, "purge", false, "also remove the data dir (irreversible)")
	serviceUninstallCmd.Flags().BoolVar(&serviceUninstallOpts.Force, "force", false, "also remove a hand-edited (non-managed) unit")
	serviceUninstallCmd.Flags().BoolVar(&serviceUninstallOpts.Local, "local", false, localFlagUsage)
}

func runServiceUninstall(cmd *cobra.Command, f Flags) error {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, serviceUninstallOpts.Yes)
	if err != nil {
		return err
	}

	systemWide, err := resolveManagedScope(deps, serviceUninstallOpts.Local)
	if err != nil {
		return err
	}

	installer, err := autostart.New(deps)
	if err != nil {
		return err
	}

	uopts := autostart.UninstallOptions{
		Purge:   serviceUninstallOpts.Purge,
		Force:   serviceUninstallOpts.Force,
		DataDir: f.DataDir,
		System:  systemWide,
	}

	if err := installer.Uninstall(context.Background(), uopts, cmd.OutOrStdout()); err != nil {
		if errors.Is(err, autostart.ErrAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
		return err
	}
	return nil
}
