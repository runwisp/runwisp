// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package autostart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
)

const (
	launchdLabelPrefix = "com.runwisp.daemon"
	launchdPlistDir    = "Library/LaunchAgents"
)

// New returns the launchd installer. macOS only ever installs a per-user
// LaunchAgent today (there is no system-wide LaunchDaemon scope yet), so
// every unit here is fingerprint-named and the fingerprint is required.
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

// ScopeCandidates implements the per-OS half of DetectScope. macOS has no
// system-wide scope, so the system path is always empty.
func ScopeCandidates(deps Deps) (systemPath, userPath string) {
	if deps.Fingerprint == "" {
		return "", ""
	}
	l := &launchdInstaller{deps: deps}
	return "", l.plistPath()
}

// CronStatus implements Installer. macOS's cron (com.vix.cron) lives under
// SIP-protected /System/Library/LaunchDaemons, so there is never anything
// RunWisp can offer to take over here.
func (l *launchdInstaller) CronStatus(_ context.Context) (string, bool, error) {
	return "", false, nil
}

// label is the per-instance launchd label, e.g.
// "com.runwisp.daemon.bright-falcon".
func (l *launchdInstaller) label() string {
	return launchdLabelPrefix + "." + l.deps.Fingerprint
}

// uid is the numeric id of the gui/<uid> launchd domain.
func (l *launchdInstaller) uid() string { return strconv.Itoa(l.deps.Euid) }

func (l *launchdInstaller) plistPath() string {
	return filepath.Join(l.deps.Home, launchdPlistDir, l.label()+".plist")
}

func (l *launchdInstaller) renderPlist(opts InstallOptions) ([]byte, string, error) {
	binarySHA, _ := fileSHA(opts.Binary)
	configHash := SettingsHash(opts)
	body, err := RenderLaunchdPlist(LaunchdParams{
		Binary:      opts.Binary,
		Config:      opts.Config,
		DataDir:     opts.DataDir,
		Host:        opts.Host,
		Port:        opts.Port,
		Home:        l.deps.Home,
		Path:        envPathDarwin(),
		LogPath:     datadir.LogPath(opts.DataDir),
		ConfigHash:  configHash,
		BinarySHA:   binarySHA,
		Label:       l.label(),
		StopTimeout: opts.StopTimeout,
	})
	return body, binarySHA, err
}

// Render returns the rendered plist without touching disk.
func (l *launchdInstaller) Render(opts InstallOptions) ([]byte, error) {
	body, _, err := l.renderPlist(opts)
	return body, err
}

func (l *launchdInstaller) ComputePlan(_ context.Context, opts InstallOptions) (Plan, error) {
	if opts.TakeOverCron {
		return Plan{}, ErrCronTakeoverUnsupported
	}
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
	uid := l.uid()
	return []Step{
		{Description: "Write LaunchAgent plist\n       " + plan.UnitPath},
		{Description: "Run:  launchctl bootout gui/" + uid + "/" + l.label() + " (best effort)"},
		{Description: "Run:  launchctl bootstrap gui/" + uid + " " + plan.UnitPath},
		{Description: "Run:  launchctl enable gui/" + uid + "/" + l.label()},
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

	if err := requireConfig(l.deps.FS, opts.Config); err != nil {
		return err
	}

	renderInstallBanner(out, plan, opts)
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

	uid := l.uid()
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

func (l *launchdInstaller) computeUninstallPlan(_ context.Context, opts UninstallOptions) (Plan, error) {
	plistPath := l.plistPath()
	plan, err := ClassifyUninstall(l.deps.FS, plistPath, opts.Force)
	if err != nil {
		return Plan{}, err
	}
	plan.UnitPath = plistPath
	if plan.Kind == PlanUninstall {
		uid := l.uid()
		plan.Steps = []Step{
			{Description: "Run:  launchctl bootout gui/" + uid + "/" + l.label()},
			{Description: "Remove plist\n       " + plistPath},
		}
	}
	return plan, nil
}

func (l *launchdInstaller) Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error {
	plan, err := l.computeUninstallPlan(ctx, opts)
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
		if err := confirmPurge(l.deps.Prompter, opts.DataDir); err != nil {
			return err
		}
	}

	uid := l.uid()
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "bootout", "gui/"+uid+"/"+l.label()); err != nil {
		fmt.Fprintf(out, "Warning: launchctl bootout: %v %s\n", err, string(stderr))
	}
	if err := l.deps.FS.Remove(plan.UnitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove plist: %w", err)
	}
	fmt.Fprintf(out, "Removed %s\n", plan.UnitPath)

	if opts.Purge {
		if err := purgeDataDir(out, opts.DataDir); err != nil {
			return err
		}
	}
	fmt.Fprintln(out, "Uninstalled.")
	return nil
}

// Stop implements Installer: asks launchd to SIGTERM the job. The daemon's
// graceful shutdown exits 0, and KeepAlive{SuccessfulExit:false} does not
// respawn successful exits, so the job stays down until login or Restart.
func (l *launchdInstaller) Stop(ctx context.Context, _ InstallOptions) error {
	uid := l.uid()
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "kill", "SIGTERM", "gui/"+uid+"/"+l.label()); err != nil {
		return fmt.Errorf("launchctl kill SIGTERM: %w: %s", err, string(stderr))
	}
	return nil
}

// Restart implements Installer: kickstart -k kills the running instance (if
// any) and starts a fresh one.
func (l *launchdInstaller) Restart(ctx context.Context, _ InstallOptions) error {
	uid := l.uid()
	if _, stderr, err := l.deps.Cmd.Run(ctx, "launchctl", "kickstart", "-k", "gui/"+uid+"/"+l.label()); err != nil {
		return fmt.Errorf("launchctl kickstart -k: %w: %s", err, string(stderr))
	}
	return nil
}

// EnsurePasswordDropIn implements Installer. launchd has no unit drop-in
// mechanism, and rewriting the plist's EnvironmentVariables to embed a secret is
// out of scope, so this is a no-op: the caller tells the operator to set
// RUNWISP_PASSWORD themselves.
func (l *launchdInstaller) EnsurePasswordDropIn(_ context.Context, _ InstallOptions, _ string) (string, bool, error) {
	return "", false, nil
}

// SupportsPasswordDropIn implements Installer: launchd has no drop-in
// mechanism, see EnsurePasswordDropIn.
func (l *launchdInstaller) SupportsPasswordDropIn() bool {
	return false
}

// WriteEnvDropIn implements Installer. launchd has no override-directory
// mechanism analogous to systemd's `<unit>.d/` — carrying RUNWISP_* into the
// plist would mean rewriting its EnvironmentVariables dict, which is out of
// scope (and would put any captured secret at the plist's 0644, not 0600).
// No-op: the caller falls back to telling the operator to set them itself.
func (l *launchdInstaller) WriteEnvDropIn(_ context.Context, _ InstallOptions, _ string, _ map[string]string) (string, DropInChange, error) {
	return "", DropInUnchanged, nil
}

func (l *launchdInstaller) Status(ctx context.Context, opts InstallOptions) (Status, error) {
	plistPath := l.plistPath()
	st := Status{
		OS:       "darwin",
		UnitPath: plistPath,
		Binary:   opts.Binary,
		DataDir:  opts.DataDir,
		Linger:   true, // N/A on macOS — LaunchAgents fire on login.
		LogsHint: "tail -f " + datadir.LogPath(opts.DataDir),
	}
	if existing, err := l.deps.FS.ReadFile(plistPath); err == nil {
		st.UnitExists = true
		parsed := extractMarkers(existing)
		st.UnitManaged = parsed.managed
		st.UnitConfigHash = parsed.configHash
		st.ExpectedBinarySHA = parsed.binarySHA
		st.Installed = parsed.managed
		st.ExpectedConfigHash = SettingsHash(opts)
	}
	st.fillBinary(opts.Binary)
	uid := l.uid()
	if stdout, _, err := l.deps.Cmd.Run(ctx, "launchctl", "print", "gui/"+uid+"/"+l.label()); err == nil {
		body := string(stdout)
		st.Running = strings.Contains(body, "state = running")
		st.Autostart = !strings.Contains(body, "disabled = true")
	}
	st.fillDataDir(l.deps.FS, opts.DataDir)
	return st, nil
}

// envPathDarwin returns the PATH the LaunchAgent will inherit.
func envPathDarwin() string {
	return envPathOrDefault("/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin")
}
