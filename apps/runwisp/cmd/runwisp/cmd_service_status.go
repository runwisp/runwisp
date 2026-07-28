// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/spf13/cobra"
)

// Exit codes — referenced by docs.
const (
	serviceStatusExitHealthy      = 0
	serviceStatusExitDegraded     = 1
	serviceStatusExitNotInstalled = 2
)

var serviceStatusOpts struct {
	System bool
}

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show autostart status (is the daemon wired into systemd / launchd?)",
	Long: `Report whether the systemd user unit (Linux/WSL) or LaunchAgent (macOS)
is installed, enabled, and running, and flag any drift between the
recorded settings hash, on-disk unit, and binary SHA.

This answers "will the daemon come up after a reboot?". For
"is the daemon alive right now?" use 'runwisp status'.

Pass --system when the unit was installed with 'service install --system'.

Exit codes:
  0  healthy (installed, enabled, running, no drift)
  1  degraded (installed but disabled / stopped / drift detected)
  2  not installed`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServiceStatus(cmd, flags)
	},
}

func init() {
	serviceStatusCmd.Flags().BoolVar(&serviceStatusOpts.System, "system", false, "the unit was installed system-wide (Linux, advanced)")
}

func runServiceStatus(cmd *cobra.Command, f Flags) error {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, true)
	if err != nil {
		return err
	}

	installer, err := autostart.New(deps)
	if err != nil {
		return err
	}

	opts, err := resolveStatusOptions(f, serviceStatusOpts.System)
	if err != nil {
		return err
	}

	st, err := installer.Status(context.Background(), opts)
	if err != nil {
		return err
	}

	exit := renderServiceStatus(cmd.OutOrStdout(), st)
	if exit != serviceStatusExitHealthy {
		os.Exit(exit)
	}
	return nil
}

// resolveStatusOptions is the lighter-weight cousin of
// resolveServiceOptions used by `service install`. Status does not
// prompt — it tolerates ambiguous defaults and only complains when a
// caller-supplied explicit value is invalid. Also reused by
// `runwisp stop` / `runwisp restart` for their service-managed probe.
// systemWide must match how the unit was installed — it picks the unit
// path and the systemctl scope, not just a display detail.
func resolveStatusOptions(f Flags, systemWide bool) (autostart.InstallOptions, error) {
	exe, err := os.Executable()
	if err != nil {
		return autostart.InstallOptions{}, fmt.Errorf("locate runwisp binary: %w", err)
	}
	return autostart.InstallOptions{
		Binary:  exe,
		Config:  f.CfgFile,
		DataDir: f.DataDir,
		Host:    f.Host,
		Port:    f.Port,
		System:  systemWide,
	}, nil
}

func renderServiceStatus(w io.Writer, st autostart.Status) int {
	fmt.Fprintln(w, "RunWisp service status")
	fmt.Fprintln(w)

	if !st.UnitExists {
		fmt.Fprintf(w, "  Installed:  no (no unit at %s)\n", st.UnitPath)
		fmt.Fprintf(w, "  Hint:       run 'runwisp service install' to wire up autostart\n")
		return serviceStatusExitNotInstalled
	}
	if !st.UnitManaged {
		fmt.Fprintf(w, "  Installed:  conflict — unit at %s is not managed by runwisp\n", st.UnitPath)
		fmt.Fprintln(w, "  Hint:       inspect by hand; 'runwisp service install --force' would overwrite")
		return serviceStatusExitDegraded
	}

	fmt.Fprintf(w, "  Installed:  yes\n")
	degraded := false
	degraded = renderAutostartLine(w, st.Autostart) || degraded
	degraded = renderRunningLine(w, st.Running) || degraded
	degraded = renderUnitFileLine(w, st) || degraded
	degraded = renderBinaryLine(w, st) || degraded
	degraded = renderDataDirLine(w, st) || degraded
	degraded = renderLingerLine(w, st) || degraded
	degraded = renderCronLine(w, st) || degraded
	renderLastStartLine(w, st)
	renderLogsHintLine(w, st)

	if degraded {
		return serviceStatusExitDegraded
	}
	return serviceStatusExitHealthy
}

func renderAutostartLine(w io.Writer, enabled bool) bool {
	if enabled {
		fmt.Fprintf(w, "  Autostart:  enabled\n")
		return false
	}
	fmt.Fprintf(w, "  Autostart:  DISABLED — will not survive reboot\n")
	return true
}

func renderRunningLine(w io.Writer, running bool) bool {
	if running {
		fmt.Fprintf(w, "  Running:    yes\n")
		return false
	}
	fmt.Fprintf(w, "  Running:    no\n")
	return true
}

func renderUnitFileLine(w io.Writer, st autostart.Status) bool {
	note := "matches recorded settings"
	drift := st.UnitConfigHash != "" && st.ExpectedConfigHash != "" && st.UnitConfigHash != st.ExpectedConfigHash
	if drift {
		note = "DRIFT — unit hash does not match current flags (rerun 'runwisp service install')"
	}
	fmt.Fprintf(w, "  Unit file:  %s  (%s)\n", st.UnitPath, note)
	return drift
}

func renderBinaryLine(w io.Writer, st autostart.Status) bool {
	note, degraded := binaryNote(st)
	fmt.Fprintf(w, "  Binary:     %s%s\n", st.Binary, note)
	return degraded
}

func binaryNote(st autostart.Status) (string, bool) {
	if !st.BinaryExists {
		return "  (binary missing on disk)", true
	}
	if st.ExpectedBinarySHA != "" && st.BinaryOnDiskSHA != "" && st.ExpectedBinarySHA != st.BinaryOnDiskSHA {
		return "  (BINARY CHANGED since install — rerun 'runwisp service install')", true
	}
	return "", false
}

func renderDataDirLine(w io.Writer, st autostart.Status) bool {
	note, degraded := dataDirNote(st)
	fmt.Fprintf(w, "  Data dir:   %s%s\n", st.DataDir, note)
	return degraded
}

func dataDirNote(st autostart.Status) (string, bool) {
	if !st.DataDirWritable {
		return "  (not writable)", true
	}
	if !st.DataDirLastWrite.IsZero() {
		return fmt.Sprintf("  (last write %s)", st.DataDirLastWrite.Format("2006-01-02 15:04:05")), false
	}
	return "", false
}

func renderLingerLine(w io.Writer, st autostart.Status) bool {
	if st.OS != "linux" {
		return false
	}
	if st.Linger {
		fmt.Fprintf(w, "  Linger:     on\n")
		return false
	}
	fmt.Fprintf(w, "  Linger:     OFF — user sessions exit will stop the daemon\n")
	return true
}

// renderCronLine shows the standing post-cutover check once this instance
// has recorded a take-over marker. Omitted entirely for an operator who
// never engaged --take-over-cron — there is nothing to report and nothing
// was probed to find that out.
func renderCronLine(w io.Writer, st autostart.Status) bool {
	if st.CronUnit == "" {
		return false
	}
	if st.CronActive {
		fmt.Fprintf(w, "  Cron:       %s active — jobs may be firing twice\n", st.CronUnit)
		return true
	}
	if st.CronMasked {
		fmt.Fprintf(w, "  Cron:       %s masked by this instance\n", st.CronUnit)
		return false
	}
	fmt.Fprintf(w, "  Cron:       %s unmasked but not running (unexpected — check by hand)\n", st.CronUnit)
	return true
}

func renderLastStartLine(w io.Writer, st autostart.Status) {
	if st.LastStart.IsZero() {
		return
	}
	fmt.Fprintf(w, "  Last start: %s\n", st.LastStart.Format("2006-01-02 15:04:05 MST"))
}

func renderLogsHintLine(w io.Writer, st autostart.Status) {
	if st.LogsHint == "" {
		return
	}
	fmt.Fprintf(w, "  Logs:       %s\n", st.LogsHint)
}
