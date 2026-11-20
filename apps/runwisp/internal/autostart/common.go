// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/netguard"
)

// hostDescription renders bind hosts as a one-line annotation for the
// install banner, so the operator immediately knows whether the unit
// will expose the daemon to the network.
func hostDescription(host string) string {
	if host == "" || netguard.IsLoopbackHost(host) {
		return "loopback only"
	}
	switch host {
	case "0.0.0.0", "::":
		return "ALL INTERFACES — accessible from the network"
	default:
		return host
	}
}

// renderInstallBanner prints the confirmation banner shown before an
// install — steps, resolved settings, and a diff on PlanUpdate. Shared
// across systemd and launchd installers. `runwisp takeover` routes through
// here too, so the headline names the consequence (taking over from cron)
// rather than the command.
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

// binaryCopyNeeded reports whether opts asks for a binary copy that has not
// already happened: a re-run against an identical target copies nothing.
func binaryCopyNeeded(opts InstallOptions) bool {
	if opts.CopyBinaryFrom == "" {
		return false
	}
	src, _ := fileSHA(opts.CopyBinaryFrom)
	dst, ok := fileSHA(opts.Binary)
	return !ok || src != dst
}

// copyBinary installs src at dst, mode 0755, owned by whoever runs this (root,
// for the system unit that asks for it). It writes a temp file beside dst and
// renames it into place: dst may be the binary the running service executes,
// which cannot be opened for writing ("text file busy"), and a reader must
// never see half a file.
func copyBinary(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op once the rename succeeded
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// sudoOwner is the account that ran sudo: whose home a copied binary may have
// come from, and who should own the link left in its place.
type sudoOwner struct {
	Home     string
	UID, GID int
}

// sudoOwnerFromEnv reads the account sudo ran for from SUDO_USER/SUDO_UID/
// SUDO_GID. ok is false when this isn't a sudo run.
func sudoOwnerFromEnv() (sudoOwner, bool) {
	name := os.Getenv("SUDO_USER")
	uid, uidErr := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, gidErr := strconv.Atoi(os.Getenv("SUDO_GID"))
	if name == "" || name == "root" || uidErr != nil || gidErr != nil {
		return sudoOwner{}, false
	}
	u, err := user.Lookup(name)
	if err != nil || u.HomeDir == "" {
		return sudoOwner{}, false
	}
	return sudoOwner{Home: u.HomeDir, UID: uid, GID: gid}, true
}

// linkUserCopy replaces src, the binary the system unit was copied from, with a
// symlink to dst when src lives in owner's home (~/.local/bin/runwisp from the
// installer). Otherwise `runwisp` and `sudo runwisp` run two binaries, and
// `runwisp upgrade` upgrades the one the service never starts. Only an exact
// copy is replaced, and the link is owned by owner like the file it replaces.
func linkUserCopy(src, dst string, owner sudoOwner) (bool, error) {
	if owner.Home == "" || !strings.HasPrefix(src, owner.Home+string(filepath.Separator)) {
		return false, nil
	}
	if info, err := os.Lstat(src); err != nil || !info.Mode().IsRegular() {
		return false, err
	}
	srcSHA, _ := fileSHA(src)
	if dstSHA, ok := fileSHA(dst); !ok || srcSHA != dstSHA {
		return false, nil
	}
	tmp := src + ".runwisp-link"
	_ = os.Remove(tmp)
	if err := os.Symlink(dst, tmp); err != nil {
		return false, err
	}
	if err := os.Lchown(tmp, owner.UID, owner.GID); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, src); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
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
