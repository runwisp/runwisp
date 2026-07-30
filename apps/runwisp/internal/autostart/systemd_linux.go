// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

// New returns the systemd installer. It deliberately does not require a
// fingerprint: the default (system-wide) install names its unit
// `runwisp.service` and never reads one. The user scope does, and says so
// at the entry points via requireFingerprint.
func New(deps Deps) (Installer, error) {
	if deps.Home == "" {
		return nil, errors.New("autostart: HOME is not set")
	}
	if deps.User == "" {
		return nil, errors.New("autostart: user is not set")
	}
	return &systemdInstaller{deps: deps}, nil
}

type systemdInstaller struct {
	deps Deps
}

// requireFingerprint guards the operations whose unit name is a function of
// the per-instance fingerprint. Only the user scope is; the system unit has
// a fixed name.
func (s *systemdInstaller) requireFingerprint(systemWide bool) error {
	if systemWide || s.deps.Fingerprint != "" {
		return nil
	}
	return errors.New("autostart: fingerprint is required for a user-scoped unit")
}

// serviceName returns the unit basename. A system-wide install drops the
// per-instance fingerprint suffix: there is exactly one system daemon per
// host, the suffix exists only to let several user-scoped daemons (several
// data dirs / cwd) coexist, and keeping it would make the unit name a
// function of the directory `service install` happened to run from — so
// `cd /etc && sudo runwisp service status` would report "not installed"
// against the unit `cd /` created.
func (s *systemdInstaller) serviceName(systemWide bool) string {
	if systemWide {
		return "runwisp.service"
	}
	return "runwisp-" + s.deps.Fingerprint + ".service"
}

// unitPath returns where the unit file will be written.
func (s *systemdInstaller) unitPath(systemWide bool) string {
	if systemWide {
		return filepath.Join(systemdSystemUnitDir, s.serviceName(systemWide))
	}
	return filepath.Join(s.deps.Home, systemdUserUnitDir, s.serviceName(systemWide))
}

// ScopeCandidates implements the per-OS half of DetectScope.
func ScopeCandidates(deps Deps) (systemPath, userPath string) {
	s := &systemdInstaller{deps: deps}
	systemPath = s.unitPath(true)
	if deps.Fingerprint != "" {
		userPath = s.unitPath(false)
	}
	return systemPath, userPath
}

// systemctlInvocation is the one place that decides how a systemctl call is
// scoped and privileged, replacing what used to be five hand-copied
// sudo-vs-`--user` branches (install, uninstall, stop/restart, and each of
// Status's four probes) that had already drifted out of sync with each
// other. Element 0 of the result is the program to run.
//
// euid is a parameter rather than an inline os.Geteuid() call so a test can
// describe a root-image machine — where "sudo" may not even be
// installed — without the test process actually running as root.
func systemctlInvocation(systemWide bool, euid int, args ...string) []string {
	if !systemWide {
		return append([]string{"systemctl", systemctlUserFlag}, args...)
	}
	if euid == 0 {
		return append([]string{"systemctl"}, args...)
	}
	return append([]string{"sudo", "systemctl"}, args...)
}

// systemctlCommandLine renders a systemctlInvocation as the text a human
// would type, for the confirmation banner and dry-run plans. Sharing
// systemctlInvocation with runSystemctl means the text shown to the
// operator can never drift from the argv actually executed.
func systemctlCommandLine(systemWide bool, euid int, args ...string) string {
	return strings.Join(systemctlInvocation(systemWide, euid, args...), " ")
}

// runSystemctl executes a systemctl call scoped per systemctlInvocation.
func (s *systemdInstaller) runSystemctl(ctx context.Context, systemWide bool, args ...string) ([]byte, []byte, error) {
	argv := systemctlInvocation(systemWide, s.deps.Euid, args...)
	return s.deps.Cmd.Run(ctx, argv[0], argv[1:]...)
}

// renderUnit assembles the SystemdParams + renders the template.
func (s *systemdInstaller) renderUnit(opts InstallOptions) ([]byte, string, error) {
	binarySHA := ""
	if data, err := os.ReadFile(opts.Binary); err == nil {
		binarySHA = hashContent(data)
	}
	configHash := SettingsHash(opts.Binary, opts.Config, opts.DataDir, opts.Host, opts.Port)
	body, err := RenderSystemdUnit(SystemdParams{
		Binary:         opts.Binary,
		Config:         opts.Config,
		DataDir:        opts.DataDir,
		Host:           opts.Host,
		Port:           opts.Port,
		Home:           s.deps.Home,
		Path:           envPath(),
		ConfigHash:     configHash,
		BinarySHA:      binarySHA,
		System:         opts.System,
		MaskedCronUnit: opts.maskedCronUnit,
	})
	return body, binarySHA, err
}

// Render returns the rendered unit file without touching disk. Used
// by `service install --print`.
func (s *systemdInstaller) Render(opts InstallOptions) ([]byte, error) {
	if err := s.requireFingerprint(opts.System); err != nil {
		return nil, err
	}
	resolved := opts
	maskedUnit, err := s.resolveMaskedCronUnit(context.Background(), opts)
	if err != nil {
		return nil, err
	}
	resolved.maskedCronUnit = maskedUnit
	body, _, err := s.renderUnit(resolved)
	return body, err
}

// ComputePlan implements Installer.
func (s *systemdInstaller) ComputePlan(ctx context.Context, opts InstallOptions) (Plan, error) {
	if err := s.requireFingerprint(opts.System); err != nil {
		return Plan{}, err
	}
	resolved := opts
	maskedUnit, err := s.resolveMaskedCronUnit(ctx, opts)
	if err != nil {
		return Plan{}, err
	}
	resolved.maskedCronUnit = maskedUnit

	desired, _, err := s.renderUnit(resolved)
	if err != nil {
		return Plan{}, err
	}
	unitPath := s.unitPath(opts.System)
	plan, err := ClassifyExisting(s.deps.FS, unitPath, desired, opts.Force)
	if err != nil {
		return Plan{}, err
	}

	// loginctl linger is a per-user-session concept; it has nothing to say
	// about a system-wide unit, which starts under PID 1 regardless.
	var lingerOn bool
	if !opts.System {
		lingerOn, _ = s.checkLinger(context.Background())
	}
	plan.UnitPath = unitPath
	plan.Binary = opts.Binary
	plan.Config = opts.Config
	plan.DataDir = opts.DataDir
	plan.Host = opts.Host
	plan.Port = opts.Port
	plan.LingerOn = lingerOn
	plan.CronUnit = maskedUnit
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
	if opts.TakeOverCron && plan.CronUnit != "" {
		steps = append(steps,
			Step{Action: ActionStopCron, Description: "Run:  " + systemctlCommandLine(true, s.deps.Euid, "stop", plan.CronUnit)},
			Step{Action: ActionMaskCron, Description: "Run:  " + systemctlCommandLine(true, s.deps.Euid, "mask", plan.CronUnit)},
		)
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
	return "Run:  " + systemctlCommandLine(systemWide, s.deps.Euid, systemctlDaemonReload)
}

func (s *systemdInstaller) enableNowCmd(systemWide bool) string {
	return "Run:  " + systemctlCommandLine(systemWide, s.deps.Euid, "enable", "--now", s.serviceName(systemWide))
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

	var cronWasActive bool
	if opts.TakeOverCron {
		var err error
		cronWasActive, err = s.stopAndMaskCron(ctx, plan.CronUnit, out)
		if err != nil {
			return fmt.Errorf("take over cron: %w", err)
		}
	}

	if err := s.runEnableNow(ctx, opts.System); err != nil {
		if opts.TakeOverCron {
			if rbErr := s.unmaskCron(ctx, plan.CronUnit, cronWasActive, out); rbErr != nil {
				fmt.Fprintf(out, "Warning: RunWisp failed to start AND cron could not be restored: %v\n", rbErr)
				fmt.Fprintf(out, "Warning: run 'sudo systemctl unmask %s' by hand to bring back a scheduler.\n", plan.CronUnit)
			}
		}
		return err
	}

	fmt.Fprintln(out, "Installed and started. `runwisp service status` to check.")

	if s.deps.WSL {
		fmt.Fprint(out, "\n"+wslTaskSchedulerPostscript()+"\n")
	}
	return nil
}

// systemctlErrLabel names a systemctl call for an error message. It omits
// the sudo prefix regardless of euid — the prefix is a privilege-escalation
// detail, not part of what failed.
func systemctlErrLabel(systemWide bool, args ...string) string {
	if systemWide {
		return "systemctl " + strings.Join(args, " ")
	}
	return "systemctl " + systemctlUserFlag + " " + strings.Join(args, " ")
}

func (s *systemdInstaller) runDaemonReload(ctx context.Context, systemWide bool) error {
	_, stderr, err := s.runSystemctl(ctx, systemWide, systemctlDaemonReload)
	if err != nil {
		return fmt.Errorf("%s: %w: %s", systemctlErrLabel(systemWide, systemctlDaemonReload), err, string(stderr))
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
	_, stderr, err := s.runSystemctl(ctx, systemWide, "enable", "--now", s.serviceName(systemWide))
	if err != nil {
		return fmt.Errorf("%s: %w: %s", systemctlErrLabel(systemWide, "enable", "--now"), err, string(stderr))
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
	if err := s.requireFingerprint(systemWide); err != nil {
		return err
	}
	_, stderr, err := s.runSystemctl(ctx, systemWide, verb, s.serviceName(systemWide))
	if err != nil {
		return fmt.Errorf("%s: %w: %s", systemctlErrLabel(systemWide, verb), err, string(stderr))
	}
	return nil
}

// ComputeUninstallPlan implements Installer.
func (s *systemdInstaller) ComputeUninstallPlan(_ context.Context, opts UninstallOptions) (Plan, error) {
	if err := s.requireFingerprint(opts.System); err != nil {
		return Plan{}, err
	}
	unitPath := s.unitPath(opts.System)
	plan, err := ClassifyUninstall(s.deps.FS, unitPath, opts.Force)
	if err != nil {
		return Plan{}, err
	}
	plan.UnitPath = unitPath
	if plan.Kind == PlanUninstall {
		name := s.serviceName(opts.System)
		plan.Steps = []Step{
			{Action: ActionStopService, Description: "Run:  " + systemctlCommandLine(opts.System, s.deps.Euid, "stop", name)},
			{Action: ActionDisableService, Description: "Run:  " + systemctlCommandLine(opts.System, s.deps.Euid, "disable", name)},
			{Action: ActionRemoveUnit, Description: "Remove unit file\n       " + unitPath},
			{Action: ActionDaemonReload, Description: "Run:  " + systemctlCommandLine(opts.System, s.deps.Euid, systemctlDaemonReload)},
		}
		// Only ever unmask a unit this instance can prove it masked —
		// the marker in its own unit file. Cron masked some other way
		// (by hand, or by a different instance) is not ours to touch.
		if unit := s.cronMarkerFromUnitFile(unitPath); unit != "" {
			plan.CronUnit = unit
			plan.Steps = append(plan.Steps, Step{
				Action:      ActionUnmaskCron,
				Description: "Run:  " + systemctlCommandLine(true, s.deps.Euid, "unmask", unit) + " (and restart it)",
			})
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
	name := s.serviceName(opts.System)
	// Stop and disable are best-effort: if the unit was already off
	// (manual stop) we still want to remove the file. We log warnings
	// but continue.
	if _, stderr, err := s.runSystemctl(ctx, opts.System, "stop", name); err != nil {
		fmt.Fprintf(out, "Warning: %s: %v %s\n", systemctlErrLabel(opts.System, "stop"), err, string(stderr))
	}
	if _, stderr, err := s.runSystemctl(ctx, opts.System, "disable", name); err != nil {
		fmt.Fprintf(out, "Warning: %s: %v %s\n", systemctlErrLabel(opts.System, "disable"), err, string(stderr))
	}
	if err := s.deps.FS.Remove(plan.UnitPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove unit: %w", err)
	}
	fmt.Fprintf(out, "Removed %s\n", plan.UnitPath)
	if _, stderr, err := s.runSystemctl(ctx, opts.System, systemctlDaemonReload); err != nil {
		fmt.Fprintf(out, "Warning: %s: %v %s\n", systemctlErrLabel(opts.System, systemctlDaemonReload), err, string(stderr))
	}
	if plan.CronUnit != "" {
		// Best-effort, like stop/disable above: RunWisp's own unit is
		// already gone, so failing to restore cron must not turn into a
		// failed uninstall — it would just leave the operator with no
		// way to remove a unit that no longer exists.
		if err := s.unmaskCron(ctx, plan.CronUnit, true, out); err != nil {
			fmt.Fprintf(out, "Warning: %v\n", err)
		}
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
	if err := s.requireFingerprint(opts.System); err != nil {
		return Status{}, err
	}
	unitPath := s.unitPath(opts.System)
	name := s.serviceName(opts.System)
	st := Status{
		OS:       "linux",
		UnitPath: unitPath,
		Binary:   opts.Binary,
		DataDir:  opts.DataDir,
		LogsHint: logsHint(opts.System, name),
	}
	if existing, err := s.deps.FS.ReadFile(unitPath); err == nil {
		st.UnitExists = true
		parsed := extractMarkers(existing)
		st.UnitManaged = parsed.managed
		st.UnitConfigHash = parsed.configHash
		st.ExpectedBinarySHA = parsed.binarySHA
		st.Installed = parsed.managed
		st.ExpectedConfigHash = SettingsHash(opts.Binary, opts.Config, opts.DataDir, opts.Host, opts.Port)
		// Only probe cron when this instance's own marker says it took
		// it over — an operator who never touches --take-over-cron pays
		// zero extra systemctl calls for this row.
		if parsed.maskedCron != "" {
			st.CronUnit = parsed.maskedCron
			if _, activeState, unitFileState, err := s.probeCronUnit(ctx, parsed.maskedCron); err == nil {
				st.CronMasked = unitFileState == "masked"
				st.CronActive = activeState == "active"
			}
		}
	}
	if data, err := os.ReadFile(opts.Binary); err == nil {
		st.BinaryExists = true
		st.BinaryOnDiskSHA = hashContent(data)
	}
	if stdout, _, err := s.runSystemctl(ctx, opts.System, "is-enabled", name); err == nil {
		st.Autostart = strings.TrimSpace(string(stdout)) == "enabled"
	}
	if stdout, _, err := s.runSystemctl(ctx, opts.System, "is-active", name); err == nil {
		st.Running = strings.TrimSpace(string(stdout)) == "active"
	}
	if stdout, _, err := s.runSystemctl(ctx, opts.System, "show", "-p", "ActiveEnterTimestamp", "--value", name); err == nil {
		st.LastStart = parseSystemdTimestamp(strings.TrimSpace(string(stdout)))
	}
	// loginctl linger is a per-user-session concept; a system-wide unit
	// starts under PID 1 and has no session to linger.
	if !opts.System {
		if lingerOn, _ := s.checkLinger(ctx); lingerOn {
			st.Linger = true
		}
	}
	if info, err := s.deps.FS.Stat(opts.DataDir); err == nil && info.IsDir() {
		st.DataDirWritable = isDirWritable(opts.DataDir)
		st.DataDirLastWrite = info.ModTime()
	}
	return st, nil
}

// logsHint renders the journalctl invocation `service status` prints. A
// system-wide unit's journal needs sudo to read (no --user scoping exists
// for it), matched to how the unit was actually installed rather than
// hardcoding --user regardless.
func logsHint(systemWide bool, name string) string {
	if systemWide {
		return "sudo journalctl -u " + name
	}
	return "journalctl --user -u " + name
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
