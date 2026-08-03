// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cutover"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

// installRequest is every decision an install needs that doesn't come from the
// global --config/--data flags. A named type, and passed down rather than read
// off the package global, because `service install` is no longer the only caller:
// `runwisp takeover` and the first-run cutover build one of these too, and a
// shared path that reached back into one command's flag block would silently pick
// up whatever that command last parsed.
type installRequest struct {
	Yes    bool
	Print  bool
	DryRun bool
	Force  bool
	Local  bool
	// Binary overrides the auto-detected binary path baked into the
	// unit. Useful for Ansible/Nix where the running binary is not
	// the one that will end up on disk.
	Binary string
}

// serviceInstallOpts holds the flags `service install` parses. They are wired in
// init() and copied into an installRequest by RunE — package-level state is OK
// here because cobra owns the singleton lifecycle.
var serviceInstallOpts installRequest

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Wire up systemd so the daemon starts on boot",
	Long: `Wire RunWisp into the host init system.

By default this installs the system-wide service: /etc/systemd/system/
runwisp.service, running as root, one per host. That needs root, so run it
with sudo. It wires up the unit and nothing else — if cron is still running
jobs on this box, the install says so and points you at ` + "`runwisp takeover`" + `,
which is the command that retires it.

Pass --local for a per-user unit instead — ~/.config/systemd/user/
runwisp-<fingerprint>.service on Linux (with linger enabled so it survives
logout), ~/Library/LaunchAgents/com.runwisp.daemon.<fingerprint>.plist on
macOS. Several of those can coexist on one host; the system service cannot.

macOS has no system-wide install yet, so --local is required there.

Re-running is idempotent: a matching unit is a no-op, a drifted unit
prompts before overwrite, a hand-edited unit refuses without --force.`,
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
}

func runServiceInstall(cmd *cobra.Command, f Flags) error {
	_, err := installService(cmd, f, serviceInstallOpts)
	return err
}

// installService is the shared install path behind `service install` and the
// first-run flow. installed reports whether a unit was actually written and
// started, so a caller that has to decide what to do next (first run choosing
// between attaching to the new service and spawning its own daemon) can tell a
// real install from an abort, a no-op, or an inspect-only run.
//
// It installs a unit and nothing else. Retiring cron used to be a flag on this
// path, which is why three commands each re-derived whether that was legal; the
// decision now lives in internal/cutover, and all this does about cron is point
// at `runwisp takeover` when one would help.
func installService(cmd *cobra.Command, f Flags, req installRequest) (installed bool, err error) {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, req.Yes)
	if err != nil {
		return false, err
	}

	systemWide, err := resolveInstallScope(req.Local, deps.Euid)
	if err != nil {
		return false, err
	}

	opts, err := resolveServiceOptions(cmd, deps, f, systemWide, req.Binary)
	if err != nil {
		return false, err
	}
	opts.Force = req.Force

	// A system unit runs as root and executes whatever the config says, so the
	// path baked into it needs the same ownership guarantee a cron source gets.
	// Checked here rather than during path resolution because `takeover` shares
	// that resolution and reports this as a plan blocker instead, so a dry run
	// can print it alongside everything else.
	if _, err := assertTrustedIfSystem(opts.Config, systemWide); err != nil {
		return false, err
	}

	installer, err := autostart.New(deps)
	if err != nil {
		return false, err
	}

	if req.Print || req.DryRun {
		return false, inspectServiceInstall(cmd, installer, opts, req, f)
	}

	settingsStale, err := preflightDaemon(context.Background(), installer, opts, f)
	if err != nil {
		return false, err
	}

	if err := installer.Install(context.Background(), opts, cmd.OutOrStdout()); err != nil {
		if errors.Is(err, autostart.ErrAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return false, nil
		}
		if errors.Is(err, autostart.ErrConfigMissing) {
			return false, &userFacingError{
				title:   "runwisp.toml is missing",
				details: "Run 'runwisp' interactively in this directory once to scaffold a starter config, then re-run 'runwisp service install'.",
			}
		}
		return false, err
	}
	if settingsStale {
		fmt.Fprintln(cmd.OutOrStdout(), staleSettingsNote)
	}
	printCronStillOwnsNote(cmd, f, deps, installer, opts)
	return true, nil
}

// printCronStillOwnsNote is what an install says about cron: nothing, unless a
// `takeover` on this box would actually do something.
//
// The condition is the take-over plan itself rather than a hand-rolled "is cron
// running" check. That is deliberate — a note pointing at a command that would
// then refuse (no jobs to find, not root, wrong OS) is worse than silence, and
// the only thing that reliably knows is the plan that command would compute.
//
// Prime directive #1 is why it exists at all: a box left with cron firing jobs
// RunWisp also reads runs them twice, and nothing else on this path would say so.
func printCronStillOwnsNote(cmd *cobra.Command, f Flags, deps autostart.Deps, installer autostart.Installer, opts autostart.InstallOptions) {
	plan, err := newCutover(f, deps, installer, opts, cutover.Options{}).Compute(context.Background())
	if err != nil || plan.Blocked() || plan.NothingToDo() || !plan.MasksCron || !plan.Evidence.CronActive {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"\nNote: %s is still running, and owns %d cron job(s) on this box.\n"+
			"      Run 'sudo runwisp takeover' to hand them to RunWisp.\n",
		plan.Evidence.CronUnit, plan.Evidence.Scan.Jobs)
}

// inspectServiceInstall serves --print and --dry-run: both answer "what
// would this install produce" and return without touching disk.
//
// --dry-run also runs the port preflight, after printing the plan. The plan
// describes the unit; the preflight describes whether the install could get as
// far as writing it, and a dry run that reported a clean plan for an install
// that stops before step one is the kind of quiet lie this flag exists to
// prevent. --print stays byte-clean for piping.
func inspectServiceInstall(cmd *cobra.Command, installer autostart.Installer, opts autostart.InstallOptions, req installRequest, f Flags) error {
	if req.Print {
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

	settingsStale, err := preflightDaemon(context.Background(), installer, opts, f)
	if err != nil {
		return err
	}
	if settingsStale {
		fmt.Fprintln(cmd.OutOrStdout(), staleSettingsNote)
	}
	return nil
}

// resolveServiceOptions turns the global --config / --data flags plus
// os.Executable() into an autostart.InstallOptions. Prompts the
// operator when the data dir / config path is ambiguous (default
// "./data" with no DB, the bare ./runwisp.toml shadowing the XDG one,
// etc.). The returned options are fully absolute — what we'd bake
// into the unit.
//
// It deliberately does not apply the system-scope trust check: `service install`
// treats an untrusted config as an error and `takeover` reports it as a plan
// blocker, so the caller decides. See assertTrustedIfSystem.
func resolveServiceOptions(cmd *cobra.Command, deps autostart.Deps, f Flags, systemWide bool, binaryOverride string) (autostart.InstallOptions, error) {
	binary, err := resolveUnitBinary(cmd.ErrOrStderr(), deps, binaryOverride)
	if err != nil {
		return autostart.InstallOptions{}, err
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

// resolveUnitBinary resolves the durable binary path to bake into the unit —
// either an explicit --binary override or this process's own executable. Shared
// with the first-run cutover, which builds its InstallOptions without cobra.
// Any non-fatal warning (a path under /tmp, a symlink that may not survive an
// upgrade) goes to stderr rather than blocking the install.
func resolveUnitBinary(stderr io.Writer, deps autostart.Deps, override string) (string, error) {
	exe := override
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate runwisp binary: %w", err)
		}
	}
	binary, warning, err := autostart.ResolveBinary(autostart.ResolveBinaryOptions{
		ExecutablePath: exe,
		EvalSymlinks:   filepath.EvalSymlinks,
		HomeDir:        deps.Home,
	})
	if err != nil {
		return "", &userFacingError{
			title:   "binary path is not durable",
			details: err.Error(),
		}
	}
	if warning != "" {
		fmt.Fprintf(stderr, "Warning: %s\n", warning)
	}
	return binary, nil
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
		return f.CfgFile, nil
	}

	xdgCfg := autostart.XDGConfigPath(deps.Home, deps.XDGConfHome)
	xdgExists := xdgCfg != "" && fileExists(xdgCfg)
	bareCfgExists := fileExists("runwisp.toml")
	return autostart.ResolveConfigPath(autostart.ResolveConfigOptions{
		Explicit:    f.CfgFile,
		ExplicitSet: cfgExplicit,
		HomeDir:     deps.Home,
		XDGConfHome: deps.XDGConfHome,
		XDGExists:   xdgExists,
		BareExists:  bareCfgExists,
	})
}

// assertTrustedIfSystem applies AssertFileTrusted to a resolved config path
// whenever the install is system-wide. That unit runs as root, so whatever
// config path ends up baked into it needs the same ownership guarantee a
// cron source already gets — otherwise `sudo runwisp service install` run
// from a directory holding someone else's runwisp.toml would silently hand
// that file root's shell.
//
// Called by the install rather than by the path resolution it shares with
// `takeover`, which reports the same condition as a plan blocker so a --dry-run
// can print it instead of dying before it says anything.
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

// preflightDaemon refuses to install while something other than this install's
// own service holds the port the unit would bind — otherwise the unit gets
// enabled and then fights the live process for the port and the SQLite file.
//
// The one holder that isn't a conflict is the reason `runwisp takeover` exists:
// on a box already running RunWisp as this very service, the port is held by the
// daemon the install is about to hand the cron jobs to. `systemctl enable --now`
// on it is a no-op and the caller reloads it afterwards, so refusing there would
// block the command a cron migration ends with.
//
// The data dir is what tells the two apart. It is what makes two daemons collide
// — one PID file, one SQLite database — and what ensureNoRunningDaemon refuses
// on, so it is also what identifies "the daemon this unit is for".
//
// settingsStale reports that our own service holds the port and the settings
// baked into its unit are about to change: systemd keeps a running unit on the
// settings it started with, so "Installed and started" would otherwise mean
// "…and still serving the old port".
func preflightDaemon(ctx context.Context, installer autostart.Installer, opts autostart.InstallOptions, f Flags) (settingsStale bool, err error) {
	bindErr := probePortAvailable(f.Host, opts.Port)
	if bindErr == nil {
		return false, nil
	}

	// A daemon bound beyond loopback withholds its paths (403), so it comes
	// back nil here and is treated like any other unidentifiable holder.
	info := probeRunwispInstance(f.Host, opts.Port)
	if info == nil || !samePath(info.DataDir, opts.DataDir) {
		return false, portConflictMessage(f.Host, opts.Port, bindErr, info)
	}

	// Our own data dir — but only the init system can say whether the process
	// on it is the service or one the operator started by hand. A failed probe
	// counts as "not the service": guessing the other way waves the install
	// through into a port fight.
	st, statusErr := installer.Status(ctx, opts)
	if statusErr != nil || !st.Installed || !st.Running {
		return false, handStartedDaemonError(info, f.Host, opts.Port)
	}
	return st.UnitConfigHash != st.ExpectedConfigHash, nil
}

// samePath compares two filesystem paths for "names the same place", the
// cheap way: absolute and lexically clean, no symlink resolution. An empty
// path matches nothing — "unknown" is not "the same".
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return absA == absB
}

// staleSettingsNote is what an install prints when it re-described a service
// that was already running: the unit on disk is current, the process is not.
const staleSettingsNote = "Note: the running daemon keeps the settings it started with — " +
	"'runwisp restart' to pick up the new unit."

// handStartedDaemonError covers the one port conflict that is nobody's mistake:
// the operator's own daemon, started by hand, holding the data dir the service
// is about to own. The generic message ("another RunWisp daemon … run on a
// different port") is wrong advice here — a different port would leave two
// daemons on one database. Stopping it is the answer, and then the service
// starts the same daemon back up under systemd.
func handStartedDaemonError(info *model.InstanceInfo, host string, port int) error {
	displayHost := host
	if displayHost == "" {
		displayHost = "127.0.0.1"
	}
	return &userFacingError{
		title: fmt.Sprintf("a RunWisp daemon started by hand (pid %d) is holding %s:%d", info.Pid, displayHost, port),
		details: fmt.Sprintf(
			"It owns the data dir this service would own (%s), so the unit could not start while it runs.\n\n"+
				"  1. Stop it:  runwisp stop --data %s\n"+
				"  2. Re-run this command — the service starts the daemon back up\n\n"+
				"Nothing has been written, and cron has not been touched.",
			info.DataDir, info.DataDir),
	}
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
