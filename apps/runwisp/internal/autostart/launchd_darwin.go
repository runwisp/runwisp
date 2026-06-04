// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package autostart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	launchdLabelPrefix = "com.runwisp.daemon"
	launchdPlistDir    = "Library/LaunchAgents"
)

// New returns the launchd installer.
func New(deps Deps) (Installer, error) {
	if deps.Home == "" {
		return nil, errors.New("autostart: HOME is not set")
	}
	if deps.User == "" {
		return nil, errors.New("autostart: user is not set")
	}
	if deps.Fingerprint == "" {
		return nil, errors.New("autostart: fingerprint is required")
	}
	return &launchdInstaller{deps: deps}, nil
}

type launchdInstaller struct {
	deps Deps
}

// label is the per-instance launchd label, e.g.
// "com.runwisp.daemon.bright-falcon".
func (l *launchdInstaller) label() string {
	return launchdLabelPrefix + "." + l.deps.Fingerprint
}

func (l *launchdInstaller) plistPath() string {
	return filepath.Join(l.deps.Home, launchdPlistDir, l.label()+".plist")
}

func (l *launchdInstaller) logPath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.log")
}

func (l *launchdInstaller) renderPlist(opts InstallOptions) ([]byte, string, error) {
	binarySHA := ""
	if data, err := os.ReadFile(opts.Binary); err == nil {
		binarySHA = hashContent(data)
	}
	configHash := SettingsHash(opts.Binary, opts.Config, opts.DataDir, opts.Host, opts.Port)
	body, err := RenderLaunchdPlist(LaunchdParams{
		Binary:     opts.Binary,
		Config:     opts.Config,
		DataDir:    opts.DataDir,
		Host:       opts.Host,
		Port:       opts.Port,
		Home:       l.deps.Home,
		Path:       envPathDarwin(),
		LogPath:    l.logPath(opts.DataDir),
		ConfigHash: configHash,
		BinarySHA:  binarySHA,
		Label:      l.label(),
	})
	return body, binarySHA, err
}

// Render returns the rendered plist without touching disk.
func (l *launchdInstaller) Render(opts InstallOptions) ([]byte, error) {
	body, _, err := l.renderPlist(opts)
	return body, err
}

func (l *launchdInstaller) ComputePlan(_ context.Context, opts InstallOptions) (Plan, error) {
	desired, _, err := l.renderPlist(opts)
	if err != nil {
		return Plan{}, err
	}
	plistPath := l.plistPath()
	plan, err := ClassifyExisting(l.deps.FS, plistPath, desired, opts.Force)
	if err != nil {
		return Plan{}, err
	}
	plan.UnitPath = plistPath
	plan.Binary = opts.Binary
	plan.Config = opts.Config
	plan.DataDir = opts.DataDir
	plan.Host = opts.Host
	plan.Port = opts.Port
	plan.Steps = l.planSteps(plan)
	return plan, nil
}

func (l *launchdInstaller) planSteps(plan Plan) []Step {
	if plan.Kind == PlanNoop || plan.Kind == PlanConflict {
		return nil
	}
	uid := strconv.Itoa(os.Getuid())
	return []Step{
		{Action: ActionWriteUnit, Description: "Write LaunchAgent plist\n       " + plan.UnitPath},
		{Action: ActionLaunchctlBootout, Description: "Run:  launchctl bootout gui/" + uid + "/" + l.label() + " (best effort)"},
		{Action: ActionLaunchctlBootstrap, Description: "Run:  launchctl bootstrap gui/" + uid + " " + plan.UnitPath},
		{Action: ActionEnableService, Description: "Run:  launchctl enable gui/" + uid + "/" + l.label()},
	}
}

func (l *launchdInstaller) Install(ctx context.Context, opts InstallOptions, out io.Writer) error {
	plan, err := l.ComputePlan(ctx, opts)
	if err != nil {
		return err
	}
	switch plan.Kind {
	case PlanConflict:
		return fmt.Errorf("%w: %s", ErrConflict, plan.UnitPath)
	case PlanNoop:
		fmt.Fprintf(out, "Already installed. ✓\n  Plist: %s\n", plan.UnitPath)
		return nil
	}

	if _, err := l.deps.FS.Stat(opts.Config); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrConfigMissing
		}
		return fmt.Errorf("stat config: %w", err)
	}

	renderInstallBanner(out, plan)
	ok, err := l.deps.Prompter.Confirm("Proceed?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}

	return l.applyInstall(ctx, plan, out)
}

func (l *launchdInstaller) applyInstall(ctx context.Context, plan Plan, out io.Writer) error {
	if err := l.deps.FS.WriteFile(plan.UnitPath, []byte(plan.UnitContent), 0644); err != nil {
		return fmt.Errorf("write plist: %w", err)
	}
	fmt.Fprintf(out, "Wrote %s\n", plan.UnitPath)

	uid := strconv.Itoa(os.Getuid())
	// bootout is best-effort — silent on first install.
	_, _, _ = l.deps.Cmd.Run(ctx, "launchctl", "bootout", "gui/"+uid+"/"+l.label())
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "bootstrap", "gui/"+uid, plan.UnitPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, string(stderr))
	}
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "enable", "gui/"+uid+"/"+l.label()); err != nil {
		return fmt.Errorf("launchctl enable: %w: %s", err, string(stderr))
	}
	fmt.Fprintln(out, "Installed and started. `runwisp service status` to check.")
	return nil
}

func (l *launchdInstaller) ComputeUninstallPlan(_ context.Context, opts UninstallOptions) (Plan, error) {
	plistPath := l.plistPath()
	plan, err := ClassifyUninstall(l.deps.FS, plistPath, opts.Force)
	if err != nil {
		return Plan{}, err
	}
	plan.UnitPath = plistPath
	if plan.Kind == PlanUninstall {
		uid := strconv.Itoa(os.Getuid())
		plan.Steps = []Step{
			{Action: ActionLaunchctlBootout, Description: "Run:  launchctl bootout gui/" + uid + "/" + l.label()},
			{Action: ActionRemoveUnit, Description: "Remove plist\n       " + plistPath},
		}
	}
	return plan, nil
}

func (l *launchdInstaller) Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error {
	plan, err := l.ComputeUninstallPlan(ctx, opts)
	if err != nil {
		return err
	}
	switch plan.Kind {
	case PlanConflict:
		return fmt.Errorf("%w: %s", ErrConflict, plan.UnitPath)
	case PlanNoop:
		fmt.Fprintf(out, "Nothing to uninstall (no plist at %s). ✓\n", plan.UnitPath)
		return nil
	}

	renderUninstallBanner(out, plan, opts)
	ok, err := l.deps.Prompter.Confirm("Proceed?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}

	if opts.Purge {
		if err := l.deps.Prompter.ConfirmLiteral(
			fmt.Sprintf("Type 'delete' to permanently remove the data dir %s:", opts.DataDir),
			"delete",
		); err != nil {
			return err
		}
	}

	uid := strconv.Itoa(os.Getuid())
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "bootout", "gui/"+uid+"/"+l.label()); err != nil {
		fmt.Fprintf(out, "Warning: launchctl bootout: %v %s\n", err, string(stderr))
	}
	if err := l.deps.FS.Remove(plan.UnitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	fmt.Fprintf(out, "Removed %s\n", plan.UnitPath)

	if opts.Purge && opts.DataDir != "" {
		if err := os.RemoveAll(opts.DataDir); err != nil {
			return fmt.Errorf("remove data dir: %w", err)
		}
		fmt.Fprintf(out, "Purged data dir %s\n", opts.DataDir)
	}
	fmt.Fprintln(out, "Uninstalled.")
	return nil
}

// Stop implements Installer: asks launchd to SIGTERM the job. The daemon's
// graceful shutdown exits 0, and KeepAlive{SuccessfulExit:false} does not
// respawn successful exits, so the job stays down until login or Restart.
func (l *launchdInstaller) Stop(ctx context.Context, _ InstallOptions) error {
	uid := strconv.Itoa(os.Getuid())
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "kill", "SIGTERM", "gui/"+uid+"/"+l.label()); err != nil {
		return fmt.Errorf("launchctl kill SIGTERM: %w: %s", err, string(stderr))
	}
	return nil
}

// Restart implements Installer: kickstart -k kills the running instance (if
// any) and starts a fresh one.
func (l *launchdInstaller) Restart(ctx context.Context, _ InstallOptions) error {
	uid := strconv.Itoa(os.Getuid())
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "kickstart", "-k", "gui/"+uid+"/"+l.label()); err != nil {
		return fmt.Errorf("launchctl kickstart -k: %w: %s", err, string(stderr))
	}
	return nil
}

func (l *launchdInstaller) Status(ctx context.Context, opts InstallOptions) (Status, error) {
	plistPath := l.plistPath()
	st := Status{
		OS:       "darwin",
		UnitPath: plistPath,
		Binary:   opts.Binary,
		DataDir:  opts.DataDir,
		Linger:   true, // N/A on macOS — LaunchAgents fire on login.
		LogsHint: "tail -f " + l.logPath(opts.DataDir),
	}
	if existing, err := l.deps.FS.ReadFile(plistPath); err == nil {
		st.UnitExists = true
		parsed := extractMarkers(existing)
		st.UnitManaged = parsed.managed
		st.UnitConfigHash = parsed.configHash
		st.ExpectedBinarySHA = parsed.binarySHA
		st.Installed = parsed.managed
		st.ExpectedConfigHash = SettingsHash(opts.Binary, opts.Config, opts.DataDir, opts.Host, opts.Port)
	}
	if data, err := os.ReadFile(opts.Binary); err == nil {
		st.BinaryExists = true
		st.BinaryOnDiskSHA = hashContent(data)
	}
	uid := strconv.Itoa(os.Getuid())
	if stdout, _, err := l.deps.Cmd.Run(ctx, "launchctl", "print", "gui/"+uid+"/"+l.label()); err == nil {
		body := string(stdout)
		st.Running = strings.Contains(body, "state = running")
		st.Autostart = !strings.Contains(body, "disabled = true")
	}
	if info, err := l.deps.FS.Stat(opts.DataDir); err == nil && info.IsDir() {
		st.DataDirWritable = isDirWritable(opts.DataDir)
		st.DataDirLastWrite = info.ModTime()
	}
	return st, nil
}

// envPathDarwin returns the PATH the LaunchAgent will inherit.
func envPathDarwin() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin"
}
