// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
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
are present. If the install took over cron, this hands it back, along with
the crontab lines 'runwisp promote' moved into runwisp.toml.

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
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), os.Stdin, serviceUninstallOpts.Yes)
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

	out := cmd.OutOrStdout()
	var impact uninstallImpact
	if hasServiceUnit(installer, f, systemWide) {
		impact = computeUninstallImpact(f.CfgFile)
		impact.print(out)
	}

	if err := installer.Uninstall(context.Background(), uopts, out); err != nil {
		if errors.Is(err, autostart.ErrAborted) {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
		return err
	}
	impact.restoreCron(out)
	return nil
}

// hasServiceUnit reports whether there is a unit to uninstall, so a no-op
// uninstall doesn't list what it would stop. Unknown counts as yes.
func hasServiceUnit(installer autostart.Installer, f Flags, systemWide bool) bool {
	opts, err := resolveStatusOptions(f, systemWide)
	if err != nil {
		return true
	}
	st, err := installer.Status(context.Background(), opts)
	return err != nil || st.UnitExists
}

// uninstallImpact is what uninstalling stops for good: the tasks and services
// runwisp.toml defines, which nothing else will run, and the crontab lines
// `promote` took off cron, which uninstall hands back to it.
type uninstallImpact struct {
	cfgPath  string
	tasks    []string
	services []string
	restores []configedit.CronRestore
	edits    map[string][]byte
}

// computeUninstallImpact is best-effort: the uninstall itself doesn't need the
// config, so one that won't load just lists nothing.
func computeUninstallImpact(cfgPath string) uninstallImpact {
	imp := uninstallImpact{cfgPath: cfgPath}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return imp
	}
	for _, t := range cfg.Tasks {
		switch {
		case t.Source == model.SourceCron:
			// cron runs it again once it is handed back
		case t.Kind.IsService():
			imp.services = append(imp.services, t.Name)
		default:
			imp.tasks = append(imp.tasks, t.Name)
		}
	}
	imp.restores, imp.edits = configedit.PlanPromotedCronRestores(cfg.CronFiles())
	return imp
}

// uninstallNamesShown caps each name list, so a big config stays readable.
const uninstallNamesShown = 10

func (imp uninstallImpact) print(out io.Writer) {
	if len(imp.tasks)+len(imp.services) > 0 {
		fmt.Fprintf(out, "Once RunWisp is uninstalled, nothing runs what %s defines:\n", imp.cfgPath)
		for _, row := range []struct {
			label string
			names []string
		}{{"services", imp.services}, {"tasks", imp.tasks}} {
			if len(row.names) == 0 {
				continue
			}
			names, more := row.names, ""
			if len(names) > uninstallNamesShown {
				names, more = names[:uninstallNamesShown], fmt.Sprintf(" and %d more", len(row.names)-uninstallNamesShown)
			}
			fmt.Fprintf(out, "  %-9s %s%s\n", row.label+":", strings.Join(names, ", "), more)
		}
		fmt.Fprintln(out)
	}
	if len(imp.restores) > 0 {
		fmt.Fprintln(out, "These crontab lines were promoted to RunWisp and go back to cron:")
		for _, r := range imp.restores {
			fmt.Fprintf(out, "  %s:%d  %s\n", r.File, r.Line, r.Text)
		}
		fmt.Fprintln(out)
	}
}

// restoreCron puts the promoted lines back. Best-effort like the rest of the
// uninstall after the unit is gone: a failure is reported, not fatal.
func (imp uninstallImpact) restoreCron(out io.Writer) {
	if len(imp.edits) == 0 {
		return
	}
	txn := configedit.New()
	for file, data := range imp.edits {
		txn.WritePreservingOwner(file, data)
	}
	if err := txn.Apply(nil); err != nil {
		fmt.Fprintf(out, "Warning: could not restore the promoted crontab lines (%v). Uncomment them by hand.\n", err)
		return
	}
	fmt.Fprintf(out, "Restored %d promoted crontab %s.\n", len(imp.restores), textutil.Pluralize(len(imp.restores), "line", "lines"))
}
