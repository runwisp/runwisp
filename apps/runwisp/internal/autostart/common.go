// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// hostDescription renders bind hosts as a one-line annotation for the
// install banner, so the operator immediately knows whether the unit
// will expose the daemon to the network.
func hostDescription(host string) string {
	switch host {
	case "127.0.0.1", "localhost", "":
		return "loopback only"
	case "0.0.0.0", "::":
		return "ALL INTERFACES — accessible from the network"
	default:
		return host
	}
}

// renderInstallBanner prints the confirmation banner shown before an
// install — steps, resolved settings, and a diff on PlanUpdate. Shared
// across systemd and launchd installers.
func renderInstallBanner(out io.Writer, plan Plan) {
	fmt.Fprintln(out, "RunWisp service install — about to perform these actions:")
	fmt.Fprintln(out)
	for i, step := range plan.Steps {
		fmt.Fprintf(out, "  %d. %s\n", i+1, step.Description)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Resolved settings:")
	fmt.Fprintf(out, "  Binary:    %s\n", plan.Binary)
	fmt.Fprintf(out, "  Config:    %s\n", plan.Config)
	fmt.Fprintf(out, "  Data dir:  %s\n", plan.DataDir)
	fmt.Fprintf(out, "  Port:      %d (%s)\n", plan.Port, hostDescription(plan.Host))
	fmt.Fprintln(out)
	if plan.Kind == PlanUpdate && plan.Diff != "" {
		fmt.Fprintln(out, "Generated content has drifted; diff:")
		fmt.Fprintln(out, plan.Diff)
	}
}

// renderUninstallBanner is the symmetric counterpart of renderInstallBanner.
func renderUninstallBanner(out io.Writer, plan Plan, opts UninstallOptions) {
	fmt.Fprintln(out, "RunWisp service uninstall — about to perform these actions:")
	fmt.Fprintln(out)
	for i, step := range plan.Steps {
		fmt.Fprintf(out, "  %d. %s\n", i+1, step.Description)
	}
	if opts.Purge {
		fmt.Fprintf(out, "  %d. Permanently delete data dir: %s\n", len(plan.Steps)+1, opts.DataDir)
	}
	fmt.Fprintln(out)
}

// isDirWritable answers the "Data dir writable?" question in
// `service status`. We probe by creating and removing a small file
// rather than reading mode bits — those lie under ACLs / SELinux.
func isDirWritable(dir string) bool {
	probe := filepath.Join(dir, ".runwisp-writable-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(probe)
	return true
}
