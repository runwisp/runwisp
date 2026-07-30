// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

// serviceInstallOpts holds flags specific to `service install`. They
// are wired in init() and read by RunE — package-level state is OK
// here because cobra owns the singleton lifecycle.
var serviceInstallOpts struct {
	Yes    bool
	Print  bool
	DryRun bool
	Force  bool
	Local  bool
	// Binary overrides the auto-detected binary path baked into the
	// unit. Useful for Ansible/Nix where the running binary is not
	// the one that will end up on disk.
	Binary string
	// TakeOverCron stops and masks the system cron service once RunWisp
	// is confirmed running (Linux, system scope, root only). It is the
	// non-interactive answer to the question offerCronTakeover asks on a
	// terminal; either way evaluateCronTakeover has the final say.
	TakeOverCron bool
	// AllowSkippedCronJobs overrides refusal #3 (a cron job that failed to
	// load) but not refusal #4 (a job this daemon cannot become the user
	// for) — that one has no override.
	AllowSkippedCronJobs bool
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Wire up systemd so the daemon starts on boot",
	Long: `Wire RunWisp into the host init system.

By default this installs the system-wide service: /etc/systemd/system/
runwisp.service, running as root, one per host. That needs root, so run it
with sudo. On a box where cron is still running and RunWisp is reading its
crontabs, the install asks whether to retire cron for you.

Pass --local for a per-user unit instead — ~/.config/systemd/user/
runwisp-<fingerprint>.service on Linux (with linger enabled so it survives
logout), ~/Library/LaunchAgents/com.runwisp.daemon.<fingerprint>.plist on
macOS. Several of those can coexist on one host; the system service cannot.

macOS has no system-wide install yet, so --local is required there.

Re-running is idempotent: a matching unit is a no-op, a drifted unit
prompts before overwrite, a hand-edited unit refuses without --force.

Flags:
  --yes        skip confirmation (CI-safe; never implies a cron take-over)
  --print      write the rendered unit to stdout and exit
  --dry-run    print the plan and exit without writing anything
  --force      overwrite a hand-edited unit
  --local      install a per-user unit instead of the system service
  --take-over-cron  stop and mask the system cron service without asking`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServiceInstall(cmd, flags)
	},
}

func init() {
	serviceInstallCmd.Flags().BoolVarP(&serviceInstallOpts.Yes, "yes", "y", false, "skip confirmation prompts")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.Print, "print", false, "print the rendered unit to stdout and exit")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.DryRun, "dry-run", false, "print the plan and exit without writing")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.Force, "force", false, "overwrite a hand-edited unit")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.Local, "local", false, localFlagUsage)
	serviceInstallCmd.Flags().StringVar(&serviceInstallOpts.Binary, "binary", "", "override the binary path baked into the unit (default: auto-detect)")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.TakeOverCron, "take-over-cron", false, "stop and mask the system cron service without asking (Linux, root, system scope)")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.AllowSkippedCronJobs, "allow-skipped-cron-jobs", false, "proceed with --take-over-cron even though some cron jobs failed to load")
}

func runServiceInstall(cmd *cobra.Command, f Flags) error {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, serviceInstallOpts.Yes)
	if err != nil {
		return err
	}

	systemWide, err := resolveInstallScope(serviceInstallOpts.Local, deps.Euid)
	if err != nil {
		return err
	}

	opts, err := resolveServiceOptions(cmd, deps, f, systemWide)
	if err != nil {
		return err
	}
	opts.Force = serviceInstallOpts.Force

	installer, err := autostart.New(deps)
	if err != nil {
		return err
	}

	// --print and --dry-run still validate an explicit --take-over-cron (so
	// they show the plan a real run would produce) but never ask: a
	// question whose answer is then thrown away is worse than no question.
	inspectOnly := serviceInstallOpts.Print || serviceInstallOpts.DryRun
	if err := resolveCronTakeover(cmd, deps, installer, &opts, !inspectOnly); err != nil {
		return err
	}

	if inspectOnly {
		return inspectServiceInstall(cmd, installer, opts)
	}

	if err := preflightDaemon(opts, f); err != nil {
		return err
	}

	if err := installer.Install(context.Background(), opts, cmd.OutOrStdout()); err != nil {
		if errors.Is(err, autostart.ErrAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
		if errors.Is(err, autostart.ErrConfigMissing) {
			return &userFacingError{
				title:   "runwisp.toml is missing",
				details: "Run 'runwisp' interactively in this directory once to scaffold a starter config, then re-run 'runwisp service install'.",
			}
		}
		return err
	}
	return nil
}

// inspectServiceInstall serves --print and --dry-run: both answer "what
// would this install produce" and return without touching disk.
func inspectServiceInstall(cmd *cobra.Command, installer autostart.Installer, opts autostart.InstallOptions) error {
	if serviceInstallOpts.Print {
		body, err := installer.Render(opts)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(body)
		return err
	}
	plan, err := installer.ComputePlan(context.Background(), opts)
	if err != nil {
		return err
	}
	printDryRun(cmd.OutOrStdout(), plan)
	return nil
}

// resolveCronTakeover decides whether this install also retires the system
// cron service, and is the only place that sets opts.TakeOverCron.
//
// Two ways in. An explicit --take-over-cron is the non-interactive answer:
// a refusal is a hard error, because the operator asked for something we
// cannot deliver and silently not doing it would leave cron and RunWisp
// both firing the same jobs. Otherwise, on a terminal, we ask — which is
// the point: an operator migrating off cron shouldn't have to already know
// a flag exists to avoid double-firing every job on the box.
func resolveCronTakeover(cmd *cobra.Command, deps autostart.Deps, installer autostart.Installer, opts *autostart.InstallOptions, mayPrompt bool) error {
	if serviceInstallOpts.TakeOverCron {
		if _, refusal := evaluateCronTakeover(opts.Config, opts.System, deps.Euid, serviceInstallOpts.AllowSkippedCronJobs); refusal != nil {
			return refusal
		}
		opts.TakeOverCron = true
		return nil
	}
	if !mayPrompt {
		return nil
	}
	return offerCronTakeover(cmd, deps, installer, opts)
}

// offerCronTakeover asks, on a terminal, whether to retire cron.
//
// It only ever asks when the answer could be yes, and only when the
// question is live: cron has to actually be running, and the config has to
// actually be reading its crontabs. Everything evaluateCronTakeover would
// refuse means we don't offer — the operator didn't ask for a take-over, so
// a refusal is not an error, just a reason to stay quiet. The one exception
// is a refusal on a host where cron *is* running: that's worth a note,
// since the operator is about to end up with two schedulers.
//
// --yes deliberately does not answer this question. Masking cron is
// destructive and not obviously reversible mid-script; a CI run that wants
// it says --take-over-cron.
func offerCronTakeover(cmd *cobra.Command, deps autostart.Deps, installer autostart.Installer, opts *autostart.InstallOptions) error {
	// Scope and privilege first: they're free, and a user-scoped install
	// has no business masking a system unit even to mention it.
	if !opts.System || deps.Euid != 0 || !deps.StdinIsTTY || deps.AutoOK {
		return nil
	}

	unit, active, err := installer.CronStatus(context.Background())
	if err != nil || unit == "" || !active {
		return nil
	}

	cfg, refusal := evaluateCronTakeover(opts.Config, opts.System, deps.Euid, serviceInstallOpts.AllowSkippedCronJobs)
	if refusal != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Note: %s is running, but RunWisp is not offering to take it over — %s\n",
			unit, refusal.title)
		return nil
	}

	ok, err := deps.Prompter.Confirm(fmt.Sprintf(
		"\n%s is running, and RunWisp is reading %d crontab(s) from this host.\n"+
			"Leaving cron up means those jobs fire twice — once from cron, once from RunWisp.\n"+
			"Stop and mask %s so RunWisp is the only scheduler?",
		unit, len(cfg.CronFiles()), unit), false)
	if err != nil {
		return err
	}
	opts.TakeOverCron = ok
	return nil
}

// evaluateCronTakeover reports whether retiring cron is something RunWisp
// can safely do for this config, returning the loaded config when it is and
// the reason when it isn't. It is pure with respect to the decision — the
// caller decides whether a refusal is an error or a reason not to ask. It
// lives here rather than in internal/autostart so that package never needs
// to import internal/config.
//
// Four refusals. The euid/scope check runs first since it is free (no
// config load); the rest run in the order an operator would want explained:
//  1. Taking over cron needs the system scope and euid 0: systemctl --user
//     has no bus for root under sudo, and a user-scoped daemon cannot
//     execute another account's jobs regardless.
//  2. Nothing is being read (no include_cron match at all) — masking cron
//     would silently stop every job on the box. Not overridable.
//  3. Any cron source failed to load (CronFinding.Skipped) — overridable
//     via --allow-skipped-cron-jobs, since the operator may already know
//     about it and want the jobs that DID load taken over anyway.
//  4. A cron task would run as a user this daemon cannot become — a
//     non-root process cannot switch OS user at all (dropping supplementary
//     groups alone needs CAP_SETGID), so the job fails at exec time on
//     every run. Not overridable: there is no flag that makes this work.
//     Refusal #1 already forces euid 0 here, and a system unit always runs
//     as root with no privilege-drop option today, so this specific check
//     cannot actually fire through this function yet — it stays as
//     defense-in-depth against a future privilege-drop option, and
//     config.RunUserFindings is unit-tested directly with a non-root euid
//     for exactly that reason.
//
// A config that fails to load is reported as a refusal rather than
// returned as an error: the caller that cares (an explicit
// --take-over-cron) surfaces it, and the install itself will fail on the
// same unreadable config a moment later with a better message.
func evaluateCronTakeover(configPath string, systemWide bool, euid int, allowSkipped bool) (*config.Config, *userFacingError) {
	if !systemWide || euid != 0 {
		return nil, &userFacingError{
			title: "taking over cron requires the system service and root",
			details: "A user-scoped daemon cannot execute another account's cron jobs, and " +
				"systemctl --user has no bus for root under sudo. Re-run as root without " +
				"--local.",
		}
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, &userFacingError{title: "cannot take over cron", details: err.Error()}
	}

	if len(cfg.CronFiles()) == 0 {
		return nil, &userFacingError{
			title: "no cron jobs are being read",
			details: "This config has no [daemon] include_cron pattern matching a crontab. " +
				"Masking the system cron service now would silently stop every job on this box. " +
				"Add include_cron first — see 'runwisp import cron' or the migrating-from-cron guide.",
		}
	}

	if !allowSkipped {
		var skipped []string
		for _, f := range cfg.CronFindings {
			if f.Skipped {
				skipped = append(skipped, f.String())
			}
		}
		if len(skipped) > 0 {
			return nil, &userFacingError{
				title: fmt.Sprintf("%d cron job(s) failed to load", len(skipped)),
				details: strings.Join(skipped, "\n") +
					"\n\nFix these first, or pass --allow-skipped-cron-jobs to take over cron anyway " +
					"(those jobs stay stopped).",
			}
		}
	}

	if names := config.RunUserFindings(cfg, euid); len(names) > 0 {
		return nil, &userFacingError{
			title: fmt.Sprintf("%d cron task(s) run as a user this daemon cannot become", len(names)),
			details: fmt.Sprintf(
				"This daemon does not run as root (uid %d), so it cannot switch to another OS user at "+
					"run time: %s. These jobs would fail on every run once cron stops running them. "+
					"Remove the 'user =' override on these tasks, or run the daemon as root.",
				euid, strings.Join(names, ", ")),
		}
	}

	return cfg, nil
}

// resolveServiceOptions turns the global --config / --data flags plus
// os.Executable() into an autostart.InstallOptions. Prompts the
// operator when the data dir / config path is ambiguous (default
// "./data" with no DB, the bare ./runwisp.toml shadowing the XDG one,
// etc.). The returned options are fully absolute — what we'd bake
// into the unit.
func resolveServiceOptions(cmd *cobra.Command, deps autostart.Deps, f Flags, systemWide bool) (autostart.InstallOptions, error) {
	exe := serviceInstallOpts.Binary
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return autostart.InstallOptions{}, fmt.Errorf("locate runwisp binary: %w", err)
		}
	}
	binary, warning, err := autostart.ResolveBinary(autostart.ResolveBinaryOptions{
		ExecutablePath: exe,
		EvalSymlinks:   filepath.EvalSymlinks,
		HomeDir:        deps.Home,
	})
	if err != nil {
		return autostart.InstallOptions{}, &userFacingError{
			title:   "binary path is not durable",
			details: err.Error(),
		}
	}
	if warning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", warning)
	}

	dataDir, err := resolveServiceDataDir(cmd, deps, f, systemWide)
	if err != nil {
		return autostart.InstallOptions{}, err
	}

	configPath, err := resolveServiceConfigPath(cmd, deps, f, systemWide)
	if err != nil {
		return autostart.InstallOptions{}, err
	}

	return autostart.InstallOptions{
		Binary:  binary,
		Config:  configPath,
		DataDir: dataDir,
		Host:    f.Host,
		Port:    f.Port,
		System:  systemWide,
	}, nil
}

// resolveServiceDataDir picks the data dir to bake into the unit. A
// system install has one canonical location (/var/lib/runwisp) — the
// same euid-derived default `runwisp daemon` itself picks with no flags
// (see resolvePathDefaults in root.go) — so an explicit --data aside,
// it's used directly rather than through the interactive XDG/bare-cwd
// resolution below, which is designed for the --local install prompt
// flow and would otherwise offer ~/.local/share/runwisp instead.
func resolveServiceDataDir(cmd *cobra.Command, deps autostart.Deps, f Flags, systemWide bool) (string, error) {
	dataDirFlag := cmd.Flag("data")
	dataDirExplicit := dataDirFlag != nil && dataDirFlag.Changed
	if systemWide && !dataDirExplicit {
		return f.DataDir, nil
	}

	bareDBExists := false
	if _, err := os.Stat(filepath.Join(".runwisp", "runwisp.db")); err == nil {
		bareDBExists = true
	}
	dataRes, err := autostart.ResolveDataDir(autostart.ResolveDataDirOptions{
		Explicit:         f.DataDir,
		ExplicitSet:      dataDirExplicit,
		HomeDir:          deps.Home,
		XDGDataHome:      deps.XDGDataHome,
		BareDefaultHasDB: bareDBExists,
	})
	if err != nil {
		return "", err
	}
	return resolveDataDirInteractive(cmd, deps, dataRes)
}

// resolveServiceConfigPath picks the config path to bake into the unit,
// mirroring resolveServiceDataDir's system-scope bypass: with no explicit
// --config, a system install uses the same /etc/runwisp/runwisp.toml
// that root.go's euid-derived default already put in f.CfgFile, rather
// than the interactive XDG/bare-cwd resolution meant for --local installs.
func resolveServiceConfigPath(cmd *cobra.Command, deps autostart.Deps, f Flags, systemWide bool) (string, error) {
	cfgFlag := cmd.Flag("config")
	cfgExplicit := cfgFlag != nil && cfgFlag.Changed
	if systemWide && !cfgExplicit {
		return assertTrustedIfSystem(f.CfgFile, systemWide)
	}

	xdgCfg := autostart.XDGConfigPath(deps.Home, deps.XDGConfHome)
	xdgExists := xdgCfg != "" && fileExists(xdgCfg)
	bareCfgExists := fileExists("runwisp.toml")
	path, err := autostart.ResolveConfigPath(autostart.ResolveConfigOptions{
		Explicit:    f.CfgFile,
		ExplicitSet: cfgExplicit,
		HomeDir:     deps.Home,
		XDGConfHome: deps.XDGConfHome,
		XDGExists:   xdgExists,
		BareExists:  bareCfgExists,
	})
	if err != nil {
		return "", err
	}
	return assertTrustedIfSystem(path, systemWide)
}

// assertTrustedIfSystem applies AssertFileTrusted to a resolved config path
// whenever the install is system-wide. That unit runs as root, so whatever
// config path ends up baked into it needs the same ownership guarantee a
// cron source already gets — otherwise `sudo runwisp service install` run
// from a directory holding someone else's runwisp.toml would silently hand
// that file root's shell.
//
// A path that doesn't exist yet is not this check's problem — preflightDaemon
// already refuses the install with ErrConfigMissing before anything is
// written, and there is no owner to distrust on a file that isn't there.
func assertTrustedIfSystem(path string, systemWide bool) (string, error) {
	if !systemWide {
		return path, nil
	}
	if _, err := os.Stat(path); err != nil {
		return path, nil
	}
	if err := config.AssertFileTrusted(path, "the config file"); err != nil {
		return "", &userFacingError{
			title:   "config file is not trusted for a system-wide install",
			details: err.Error(),
		}
	}
	return path, nil
}

// resolveDataDirInteractive folds in operator confirmation when
// ResolveDataDir asked for one. Returns the final absolute path.
func resolveDataDirInteractive(cmd *cobra.Command, deps autostart.Deps, res autostart.ResolveDataDirResult) (string, error) {
	switch res.Action {
	case autostart.ResolveActionAccept:
		return res.Path, nil
	case autostart.ResolveActionNotice:
		fmt.Fprintln(cmd.ErrOrStderr(), res.Detail)
		return res.Path, nil
	case autostart.ResolveActionWarn:
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s\n", res.Detail)
		return res.Path, nil
	case autostart.ResolveActionReject:
		return "", &userFacingError{title: res.Detail}
	case autostart.ResolveActionPrompt:
		ok, err := deps.Prompter.Confirm(res.Detail, true)
		if err != nil {
			return "", err
		}
		if ok {
			return res.Path, nil
		}
		// Declined the suggested location — rather than dead-ending, offer the
		// current directory (the common "install right here" intent) before
		// giving up.
		return resolveDataDirCurrentDir(cmd, deps)
	}
	return res.Path, nil
}

// dataDirDeclinedError is the give-up message when the operator wants neither the
// suggested location nor the current directory. It names both frictionless ways
// to pin one.
var dataDirDeclinedError = &userFacingError{
	title: "data dir choice declined",
	details: "Choose where the daemon should store its data:\n" +
		"  - Use the current directory:  runwisp service install --data .\n" +
		"  - Or pin an absolute path:    runwisp service install --data /abs/path",
}

// resolveDataDirCurrentDir offers the current working directory as the data dir
// after the operator declined the suggested one. It re-uses ResolveDataDir (with
// an explicit ".") so the current dir passes the same durability guards — a cwd
// under /tmp is refused, and the offer is skipped rather than baking a doomed
// path into the unit.
func resolveDataDirCurrentDir(cmd *cobra.Command, deps autostart.Deps) (string, error) {
	cwdRes, err := autostart.ResolveDataDir(autostart.ResolveDataDirOptions{
		Explicit:    ".",
		ExplicitSet: true,
		HomeDir:     deps.Home,
		XDGDataHome: deps.XDGDataHome,
	})
	if err != nil || cwdRes.Action == autostart.ResolveActionReject {
		return "", dataDirDeclinedError
	}
	ok, err := deps.Prompter.Confirm(fmt.Sprintf("Use the current directory (%s) instead?", cwdRes.Path), true)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", dataDirDeclinedError
	}
	return cwdRes.Path, nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// preflightDaemon refuses to install while a daemon is already up
// against the same data dir. Without this guard, the install path
// would race the live process and end up double-binding the port.
func preflightDaemon(opts autostart.InstallOptions, f Flags) error {
	if bindErr := probePortAvailable(f.Host, opts.Port); bindErr != nil {
		return nonInteractivePortConflict(f.Host, opts.Port, bindErr)
	}
	return nil
}

// printDryRun emits a plan summary for --dry-run.
func printDryRun(w interface{ Write([]byte) (int, error) }, plan autostart.Plan) {
	fmt.Fprintf(w, "Plan: %s\n", plan.Kind)
	fmt.Fprintf(w, "Reason: %s\n", plan.Reason)
	fmt.Fprintln(w)
	for i, step := range plan.Steps {
		fmt.Fprintf(w, "  %d. %s\n", i+1, step.Description)
	}
	if plan.Diff != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Diff:")
		fmt.Fprintln(w, plan.Diff)
	}
}
