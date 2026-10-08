// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
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
//
// The headline names what is about to happen rather than the command that asked
// for it. `runwisp takeover` routes through this same install path, and reading
// back "service install" from a command whose entire point is retiring cron told
// the operator about the mechanism instead of the consequence.
func renderInstallBanner(out io.Writer, plan Plan, opts InstallOptions) {
	if opts.TakeOverCron && plan.CronUnit != "" {
		fmt.Fprintf(out, "RunWisp is taking over from %s — about to perform these actions:\n", plan.CronUnit)
	} else {
		fmt.Fprintln(out, "RunWisp service install — about to perform these actions:")
	}
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

// fileSHA hashes the file at path; ok is false when it cannot be read.
func fileSHA(path string) (sha string, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return hashContent(data), true
}

func (st *Status) fillBinary(path string) {
	if sha, ok := fileSHA(path); ok {
		st.BinaryExists = true
		st.BinaryOnDiskSHA = sha
	}
}

func (st *Status) fillDataDir(fsys FileSystem, dir string) {
	if info, err := fsys.Stat(dir); err == nil && info.IsDir() {
		st.DataDirWritable = isDirWritable(dir)
		st.DataDirLastWrite = info.ModTime()
	}
}

// requireConfig returns ErrConfigMissing when the unit would point at a config
// that does not exist.
func requireConfig(fsys FileSystem, path string) error {
	if _, err := fsys.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrConfigMissing
		}
		return fmt.Errorf("stat config: %w", err)
	}
	return nil
}

// confirmPurge is the --purge footgun guard: the operator must type the word,
// and --yes does not answer for them.
func confirmPurge(p Prompter, dataDir string) error {
	return p.ConfirmLiteral(
		fmt.Sprintf("Type 'delete' to permanently remove the data dir %s:", dataDir),
		"delete",
	)
}

func purgeDataDir(out io.Writer, dataDir string) error {
	if dataDir == "" {
		return nil
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("remove data dir %s: %w", dataDir, err)
	}
	fmt.Fprintf(out, "Purged data dir %s\n", dataDir)
	return nil
}
