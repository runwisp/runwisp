// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package autostart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	systemdUserUnitDir   = ".config/systemd/user"
	systemdSystemUnitDir = "/etc/systemd/system"

	// defaultPATH mirrors the env var most non-login shells inherit.
	// systemd does not propagate a $PATH to user units, so the unit
	// has to set its own.
	defaultPATH = "/usr/local/bin:/usr/bin:/bin"

	// systemctlUserFlag is the systemctl flag that scopes commands to
	// the calling user's manager (as opposed to the system manager).
	systemctlUserFlag = "--user"

	// systemctlDaemonReload is the systemctl subcommand that re-reads
	// unit files from disk after install/uninstall.
	systemctlDaemonReload = "daemon-reload"
)

// New returns the systemd installer.
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
	return &systemdInstaller{deps: deps}, nil
}

type systemdInstaller struct {
	deps Deps
}

// serviceName returns the per-instance unit basename, e.g.
// "runwisp-bright-falcon.service". The suffix lets multiple RunWisp
// daemons coexist on one host without clobbering each other.
func (s *systemdInstaller) serviceName() string {
	return "runwisp-" + s.deps.Fingerprint + ".service"
}

// unitPath returns where the unit file will be written.
func (s *systemdInstaller) unitPath(systemWide bool) string {
	if systemWide {
		return filepath.Join(systemdSystemUnitDir, s.serviceName())
	}
	return filepath.Join(s.deps.Home, systemdUserUnitDir, s.serviceName())
}

// renderUnit assembles the SystemdParams + renders the template.
func (s *systemdInstaller) renderUnit(opts InstallOptions) ([]byte, string, error) {
	binarySHA := ""
	if data, err := os.ReadFile(opts.Binary); err == nil {
		binarySHA = hashContent(data)
	}
	configHash := SettingsHash(opts.Binary, opts.Config, opts.DataDir, opts.Host, opts.Port)
	body, err := RenderSystemdUnit(SystemdParams{
		Binary:     opts.Binary,
		Config:     opts.Config,
		DataDir:    opts.DataDir,
		Host:       opts.Host,
		Port:       opts.Port,
		Home:       s.deps.Home,
		Path:       envPath(),
		ConfigHash: configHash,
		BinarySHA:  binarySHA,
	})
	return body, binarySHA, err
}

// Render returns the rendered unit file without touching disk. Used
// by `service install --print`.
func (s *systemdInstaller) Render(opts InstallOptions) ([]byte, error) {
	body, _, err := s.renderUnit(opts)
	return body, err
}

// ComputePlan implements Installer.
func (s *systemdInstaller) ComputePlan(_ context.Context, opts InstallOptions) (Plan, error) {
	desired, _, err := s.renderUnit(opts)
	if err != nil {
		return Plan{}, err
	}
	unitPath := s.unitPath(opts.System)
	plan, err := ClassifyExisting(s.deps.FS, unitPath, desired, opts.Force)
	if err != nil {
		return Plan{}, err
	}

	lingerOn, _ := s.checkLinger(context.Background())
	plan.UnitPath = unitPath
	plan.Binary = opts.Binary
	plan.Config = opts.Config
	plan.DataDir = opts.DataDir
	plan.Host = opts.Host
	plan.Port = opts.Port
	plan.LingerOn = lingerOn
	plan.Steps = s.planSteps(plan, opts)
	return plan, nil
}

// planSteps lists the actions for a plan in display order.
func (s *systemdInstaller) planSteps(plan Plan, opts InstallOptions) []Step {
	if plan.Kind == PlanNoop || plan.Kind == PlanConflict {
		return nil
	}
	steps := []Step{
		{
			Action:      ActionWriteUnit,
			Description: "Write unit file\n       " + plan.UnitPath,
		},
		{
			Action:      ActionDaemonReload,
			Description: s.daemonReloadCmd(opts.System),
		},
	}
	if !opts.System && !plan.LingerOn {
		steps = append(steps, Step{
			Action:      ActionEnableLinger,
			Description: fmt.Sprintf("Run:  loginctl enable-linger %s         ← needs sudo", s.deps.User),
		})
	}
	steps = append(steps, Step{
		Action:      ActionEnableService,
		Description: s.enableNowCmd(opts.System),
	})
	if s.deps.WSL {
		steps = append(steps, Step{
			Action:      ActionPrintWSLPostscript,
			Description: "Print Windows Task Scheduler PowerShell snippet (Windows-side autostart)",
		})
	}
	return steps
}

func (s *systemdInstaller) daemonReloadCmd(systemWide bool) string {
	if systemWide {
		return "Run:  sudo systemctl " + systemctlDaemonReload
	}
	return "Run:  systemctl " + systemctlUserFlag + " " + systemctlDaemonReload
}

func (s *systemdInstaller) enableNowCmd(systemWide bool) string {
	if systemWide {
		return "Run:  sudo systemctl enable --now " + s.serviceName()
	}
	return "Run:  systemctl " + systemctlUserFlag + " enable --now " + s.serviceName()
}

// Install implements Installer.
func (s *systemdInstaller) Install(ctx context.Context, opts InstallOptions, out io.Writer) error {
	plan, err := s.ComputePlan(ctx, opts)
	if err != nil {
		return err
	}

	switch plan.Kind {
	case PlanConflict:
		return fmt.Errorf("%w: %s", ErrConflict, plan.UnitPath)
	case PlanNoop:
		fmt.Fprintf(out, "Already installed. ✓\n  Unit: %s\n", plan.UnitPath)
		return nil
	}

	if err := s.preflight(ctx, opts); err != nil {
		return err
	}

	renderInstallBanner(out, plan)

	ok, err := s.deps.Prompter.Confirm("Proceed?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}

	return s.applyInstall(ctx, plan, opts, out)
}

// preflight gates the install on conditions that are catch-this-now-or-
// boot-loop-later: missing config, port already taken, daemon running.
func (s *systemdInstaller) preflight(_ context.Context, opts InstallOptions) error {
	if _, err := s.deps.FS.Stat(opts.Config); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ErrConfigMissing
		}
		return fmt.Errorf("stat config: %w", err)
	}
	return nil
}

// applyInstall executes the steps after confirmation.
func (s *systemdInstaller) applyInstall(ctx context.Context, plan Plan, opts InstallOptions, out io.Writer) error {
	if err := s.deps.FS.WriteFile(plan.UnitPath, []byte(plan.UnitContent), 0644); err != nil {
		return fmt.Errorf("write unit: %w", err)
	}
	fmt.Fprintf(out, "Wrote %s\n", plan.UnitPath)

	if err := s.runDaemonReload(ctx, opts.System); err != nil {
		return err
	}

	if !opts.System && !plan.LingerOn {
		if err := s.enableLinger(ctx, out); err != nil {
			return err
		}
	}

	if err := s.runEnableNow(ctx, opts.System); err != nil {
		return err
	}

	fmt.Fprintln(out, "Installed and started. `runwisp service status` to check.")

	if s.deps.WSL {
		fmt.Fprint(out, "\n"+wslTaskSchedulerPostscript()+"\n")
	}
	return nil
}

func (s *systemdInstaller) runDaemonReload(ctx context.Context, systemWide bool) error {
	if systemWide {
		_, stderr, err := s.deps.Cmd.Run(ctx, "sudo", "systemctl", systemctlDaemonReload)
		if err != nil {
			return fmt.Errorf("systemctl daemon-reload: %w: %s", err, string(stderr))
		}
		return nil
	}
	_, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, systemctlDaemonReload)
	if err != nil {
		return fmt.Errorf("systemctl --user daemon-reload: %w: %s", err, string(stderr))
	}
	return nil
}

func (s *systemdInstaller) enableLinger(ctx context.Context, out io.Writer) error {
	fmt.Fprintf(out, "Running: sudo loginctl enable-linger %s\n", s.deps.User)
	_, stderr, err := s.deps.Cmd.Run(ctx, "sudo", "loginctl", "enable-linger", s.deps.User)
	if err != nil {
		return fmt.Errorf("loginctl enable-linger: %w: %s", err, string(stderr))
	}
	return nil
}

func (s *systemdInstaller) runEnableNow(ctx context.Context, systemWide bool) error {
	if systemWide {
		_, stderr, err := s.deps.Cmd.Run(ctx, "sudo", "systemctl", "enable", "--now", s.serviceName())
		if err != nil {
			return fmt.Errorf("systemctl enable --now: %w: %s", err, string(stderr))
		}
		return nil
	}
	_, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "enable", "--now", s.serviceName())
	if err != nil {
		return fmt.Errorf("systemctl --user enable --now: %w: %s", err, string(stderr))
	}
	return nil
}

// Stop implements Installer: stops the unit without disabling it.
func (s *systemdInstaller) Stop(ctx context.Context, opts InstallOptions) error {
	return s.runSystemctlVerb(ctx, opts.System, "stop")
}

// Restart implements Installer.
func (s *systemdInstaller) Restart(ctx context.Context, opts InstallOptions) error {
	return s.runSystemctlVerb(ctx, opts.System, "restart")
}

func (s *systemdInstaller) runSystemctlVerb(ctx context.Context, systemWide bool, verb string) error {
	if systemWide {
		_, stderr, err := s.deps.Cmd.Run(ctx, "sudo", "systemctl", verb, s.serviceName())
		if err != nil {
			return fmt.Errorf("sudo systemctl %s: %w: %s", verb, err, string(stderr))
		}
		return nil
	}
	_, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, verb, s.serviceName())
	if err != nil {
		return fmt.Errorf("systemctl --user %s: %w: %s", verb, err, string(stderr))
	}
	return nil
}

// ComputeUninstallPlan implements Installer.
func (s *systemdInstaller) ComputeUninstallPlan(_ context.Context, opts UninstallOptions) (Plan, error) {
	unitPath := s.unitPath(false)
	plan, err := ClassifyUninstall(s.deps.FS, unitPath, opts.Force)
	if err != nil {
		return Plan{}, err
	}
	plan.UnitPath = unitPath
	if plan.Kind == PlanUninstall {
		plan.Steps = []Step{
			{Action: ActionStopService, Description: "Run:  systemctl " + systemctlUserFlag + " stop " + s.serviceName()},
			{Action: ActionDisableService, Description: "Run:  systemctl " + systemctlUserFlag + " disable " + s.serviceName()},
			{Action: ActionRemoveUnit, Description: "Remove unit file\n       " + unitPath},
			{Action: ActionDaemonReload, Description: "Run:  systemctl " + systemctlUserFlag + " " + systemctlDaemonReload},
		}
	}
	return plan, nil
}

// Uninstall implements Installer.
func (s *systemdInstaller) Uninstall(ctx context.Context, opts UninstallOptions, out io.Writer) error {
	plan, err := s.ComputeUninstallPlan(ctx, opts)
	if err != nil {
		return err
	}

	switch plan.Kind {
	case PlanConflict:
		return fmt.Errorf("%w: %s", ErrConflict, plan.UnitPath)
	case PlanNoop:
		fmt.Fprintf(out, "Nothing to uninstall (no unit at %s). ✓\n", plan.UnitPath)
		return nil
	}

	renderUninstallBanner(out, plan, opts)
	ok, err := s.deps.Prompter.Confirm("Proceed?", false)
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}

	if opts.Purge {
		// --purge is the footgun; the literal word check happens
		// in the CLI before we get here (it doesn't share the
		// auto-yes path).
		if err := s.deps.Prompter.ConfirmLiteral(
			fmt.Sprintf("Type 'delete' to permanently remove the data dir %s:", opts.DataDir),
			"delete",
		); err != nil {
			return err
		}
	}

	return s.applyUninstall(ctx, plan, opts, out)
}

func (s *systemdInstaller) applyUninstall(ctx context.Context, plan Plan, opts UninstallOptions, out io.Writer) error {
	// Stop and disable are best-effort: if the unit was already off
	// (manual stop) we still want to remove the file. We log warnings
	// but continue.
	if _, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "stop", s.serviceName()); err != nil {
		fmt.Fprintf(out, "Warning: systemctl --user stop: %v %s\n", err, string(stderr))
	}
	if _, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "disable", s.serviceName()); err != nil {
		fmt.Fprintf(out, "Warning: systemctl --user disable: %v %s\n", err, string(stderr))
	}
	if err := s.deps.FS.Remove(plan.UnitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove unit: %w", err)
	}
	fmt.Fprintf(out, "Removed %s\n", plan.UnitPath)
	if _, stderr, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, systemctlDaemonReload); err != nil {
		fmt.Fprintf(out, "Warning: systemctl --user daemon-reload: %v %s\n", err, string(stderr))
	}
	if opts.Purge && opts.DataDir != "" {
		if err := os.RemoveAll(opts.DataDir); err != nil {
			return fmt.Errorf("remove data dir %s: %w", opts.DataDir, err)
		}
		fmt.Fprintf(out, "Purged data dir %s\n", opts.DataDir)
	}
	fmt.Fprintln(out, "Uninstalled.")
	return nil
}

// Status implements Installer.
func (s *systemdInstaller) Status(ctx context.Context, opts InstallOptions) (Status, error) {
	unitPath := s.unitPath(opts.System)
	st := Status{
		OS:       "linux",
		UnitPath: unitPath,
		Binary:   opts.Binary,
		DataDir:  opts.DataDir,
		LogsHint: "journalctl --user -u " + s.serviceName(),
	}
	if existing, err := s.deps.FS.ReadFile(unitPath); err == nil {
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
	if stdout, _, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "is-enabled", s.serviceName()); err == nil {
		st.Autostart = strings.TrimSpace(string(stdout)) == "enabled"
	}
	if stdout, _, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "is-active", s.serviceName()); err == nil {
		st.Running = strings.TrimSpace(string(stdout)) == "active"
	}
	if stdout, _, err := s.deps.Cmd.Run(ctx, "systemctl", systemctlUserFlag, "show", "-p", "ActiveEnterTimestamp", "--value", s.serviceName()); err == nil {
		st.LastStart = parseSystemdTimestamp(strings.TrimSpace(string(stdout)))
	}
	if lingerOn, _ := s.checkLinger(ctx); lingerOn {
		st.Linger = true
	}
	if info, err := s.deps.FS.Stat(opts.DataDir); err == nil && info.IsDir() {
		st.DataDirWritable = isDirWritable(opts.DataDir)
		st.DataDirLastWrite = info.ModTime()
	}
	return st, nil
}

// checkLinger reports whether loginctl has linger enabled for this user.
func (s *systemdInstaller) checkLinger(ctx context.Context) (bool, error) {
	stdout, _, err := s.deps.Cmd.Run(ctx, "loginctl", "show-user", "--property=Linger", s.deps.User)
	if err != nil {
		return false, err
	}
	return strings.Contains(string(stdout), "Linger=yes"), nil
}

// envPath returns the PATH the unit will inherit.
func envPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return defaultPATH
}

// parseSystemdTimestamp parses the ActiveEnterTimestamp emitted by
// `systemctl show --value`. Empty/zero responses return the zero time.
func parseSystemdTimestamp(s string) time.Time {
	if s == "" || s == "n/a" || strings.HasPrefix(s, "0") {
		return time.Time{}
	}
	// "Mon 2024-09-30 12:34:56 UTC"
	layouts := []string{
		"Mon 2006-01-02 15:04:05 MST",
		"2006-01-02 15:04:05 MST",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// wslTaskSchedulerPostscript is printed verbatim on WSL installs so
// the operator can wire up the Windows-side autostart.
func wslTaskSchedulerPostscript() string {
	distro := os.Getenv("WSL_DISTRO_NAME")
	if distro == "" {
		distro = "<distro>"
	}
	return strings.TrimSpace(`
─────────────────────────────────────────────────────────────────────────
WSL detected. The Linux-side systemd user unit is installed, but Windows
also needs a hook so the WSL distro boots on login. Run this in PowerShell:

  $Action  = New-ScheduledTaskAction -Execute 'wsl.exe' -Argument '~ -d `+distro+` -- true'
  $Trigger = New-ScheduledTaskTrigger -AtLogOn
  Register-ScheduledTask -TaskName 'RunWispBoot' -Action $Action -Trigger $Trigger

─────────────────────────────────────────────────────────────────────────
`) + "\n"
}
