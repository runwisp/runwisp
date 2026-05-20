// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

var serviceStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show autostart status (is the daemon wired into systemd / launchd?)",
	Long: `Report whether the systemd user unit (Linux/WSL) or LaunchAgent (macOS)
is installed, enabled, and running, and flag any drift between the
recorded settings hash, on-disk unit, and binary SHA.

This answers "will the daemon come up after a reboot?". For
"is the daemon alive right now?" use 'runwisp status'.

Exit codes:
  0  healthy (installed, enabled, running, no drift)
  1  degraded (installed but disabled / stopped / drift detected)
  2  not installed`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServiceStatus(cmd)
	},
}

func runServiceStatus(cmd *cobra.Command) error {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, true)
	if err != nil {
		return err
	}

	installer, err := autostart.New(deps)
	if err != nil {
		return err
	}

	opts, err := resolveStatusOptions(cmd, deps)
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
// caller-supplied explicit value is invalid.
func resolveStatusOptions(_ *cobra.Command, deps autostart.Deps) (autostart.InstallOptions, error) {
	exe, err := os.Executable()
	if err != nil {
		return autostart.InstallOptions{}, fmt.Errorf("locate runwisp binary: %w", err)
	}
	return autostart.InstallOptions{
		Binary:  exe,
		Config:  flags.CfgFile,
		DataDir: flags.DataDir,
		Host:    flags.Host,
		Port:    flags.Port,
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

	degraded := false

	fmt.Fprintf(w, "  Installed:  yes\n")
	if st.Autostart {
		fmt.Fprintf(w, "  Autostart:  enabled\n")
	} else {
		fmt.Fprintf(w, "  Autostart:  DISABLED — will not survive reboot\n")
		degraded = true
	}
	if st.Running {
		fmt.Fprintf(w, "  Running:    yes\n")
	} else {
		fmt.Fprintf(w, "  Running:    no\n")
		degraded = true
	}

	unitNote := "matches recorded settings"
	if st.UnitConfigHash != "" && st.ExpectedConfigHash != "" && st.UnitConfigHash != st.ExpectedConfigHash {
		unitNote = "DRIFT — unit hash does not match current flags (rerun 'runwisp service install')"
		degraded = true
	}
	fmt.Fprintf(w, "  Unit file:  %s  (%s)\n", st.UnitPath, unitNote)

	binNote := ""
	if st.BinaryExists && st.ExpectedBinarySHA != "" && st.BinaryOnDiskSHA != "" &&
		st.ExpectedBinarySHA != st.BinaryOnDiskSHA {
		binNote = "  (BINARY CHANGED since install — rerun 'runwisp service install')"
		degraded = true
	}
	if !st.BinaryExists {
		binNote = "  (binary missing on disk)"
		degraded = true
	}
	fmt.Fprintf(w, "  Binary:     %s%s\n", st.Binary, binNote)

	ddNote := ""
	if !st.DataDirWritable {
		ddNote = "  (not writable)"
		degraded = true
	} else if !st.DataDirLastWrite.IsZero() {
		ddNote = fmt.Sprintf("  (last write %s)", st.DataDirLastWrite.Format("2006-01-02 15:04:05"))
	}
	fmt.Fprintf(w, "  Data dir:   %s%s\n", st.DataDir, ddNote)

	if st.OS == "linux" {
		if st.Linger {
			fmt.Fprintf(w, "  Linger:     on\n")
		} else {
			fmt.Fprintf(w, "  Linger:     OFF — user sessions exit will stop the daemon\n")
			degraded = true
		}
	}

	if !st.LastStart.IsZero() {
		fmt.Fprintf(w, "  Last start: %s\n", st.LastStart.Format("2006-01-02 15:04:05 MST"))
	}
	if st.LogsHint != "" {
		fmt.Fprintf(w, "  Logs:       %s\n", st.LogsHint)
	}

	if degraded {
		return serviceStatusExitDegraded
	}
	return serviceStatusExitHealthy
}
