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
	"strings"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cutover"
	"github.com/runwisp/runwisp/internal/datadir"
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
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), os.Stdin, req.Yes)
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
	ensureServiceEnv(cmd, installer, opts)
	printCronStillOwnsNote(cmd, f, deps, installer, opts)
	return true, nil
}

// manualPasswordHint is the fallback advice when RunWisp can't set the password
// itself (an OS without a drop-in mechanism, or a failure writing one).
const manualPasswordHint = "Set a stable Web UI password with RUNWISP_PASSWORD; without it the daemon\n" +
	"generates a new one every boot. See https://docs.runwisp.com/operations/auth/#the-password"

// capturedServiceEnv scans the install shell's environment for the RUNWISP_*
// vars to carry into the managed service (RUNWISP_AUTH, RUNWISP_TLS, an
// operator-supplied RUNWISP_PASSWORD, RUNWISP_CLOUD_TOKEN, …) — a systemd
// unit or launchd plist never inherits the invoking shell's environment, so
// without this every one of them would silently vanish on install.
// RUNWISP_SERVICE_MANAGED is excluded: it's the marker the generated unit
// sets on the daemon's own behalf, never legitimate operator input.
func capturedServiceEnv() map[string]string {
	const prefix = "RUNWISP_"
	vars := map[string]string{}
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, prefix) || key == autostart.ServiceManagedEnv {
			continue
		}
		vars[key] = value
	}
	return vars
}

// ensureServiceEnv gives a fresh service install the operator's RUNWISP_*
// environment and a stable Web UI password — neither survives on its own,
// since a systemd unit / launchd plist never inherits the install shell's
// environment.
//
// Every RUNWISP_* var present at install time (an operator-supplied
// RUNWISP_PASSWORD included) is refreshed into a 0600 drop-in beside the
// unit on every install, so changing RUNWISP_AUTH/RUNWISP_TLS and
// reinstalling actually takes effect. When auth is on and the operator did
// not set RUNWISP_PASSWORD, a password is generated and persisted the same
// way into its own 0600 drop-in, so a managed daemon does not mint a fresh
// one — logging every session out — on every restart.
//
// Best-effort and idempotent: any failure here leaves a working install
// rather than aborting the command. The daemon is restarted once at the end
// if either drop-in actually changed, so the new environment takes effect.
func ensureServiceEnv(cmd *cobra.Command, installer autostart.Installer, opts autostart.InstallOptions) {
	out := cmd.OutOrStdout()
	restartNeeded := false

	envPath, change, err := installer.WriteEnvDropIn(context.Background(), opts, autostart.EnvDropInName, capturedServiceEnv())
	switch {
	case err != nil:
		fmt.Fprintf(out, "\nNote: could not carry your RUNWISP_* environment into the service (%v).\n", err)
	case change == autostart.DropInWritten:
		fmt.Fprintf(out, "\nSaved your RUNWISP_* environment to %s (0600).\n", envPath)
		restartNeeded = true
	case change == autostart.DropInRemoved:
		printEnvDropInRemovedWarning(out, envPath)
		restartNeeded = true
	}

	if ensureServicePasswordFallback(out, installer, opts) {
		restartNeeded = true
	}

	if restartNeeded {
		if err := installer.Restart(context.Background(), opts); err != nil {
			fmt.Fprintf(out, "\nWrote service environment changes, but restarting to apply them failed (%v).\n"+
				"Run 'runwisp restart' and it takes effect.\n", err)
		}
	}
}

// printEnvDropInRemovedWarning fires when this install's shell had no RUNWISP_*
// vars set, so WriteEnvDropIn just deleted a previous install's captured
// environment instead of refreshing it — silently reverting whatever it
// carried (RUNWISP_AUTH=off included) back to its default. A plain "removed a
// file" note is not enough for a change this consequential: it can flip auth
// back on and mint a brand-new password in the same breath, so it gets the
// same unmissable banner treatment as printNoAuthBanner.
func printEnvDropInRemovedWarning(out io.Writer, path string) {
	var b strings.Builder
	b.WriteString("\n================================================================================\n")
	b.WriteString("  WARNING: no RUNWISP_* variables were found in this shell, so the\n")
	fmt.Fprintf(&b, "  environment a previous install saved to %s\n", path)
	b.WriteString("  was just removed. Any setting it carried (RUNWISP_AUTH, RUNWISP_TLS,\n")
	b.WriteString("  RUNWISP_CLOUD_TOKEN, ...) has reverted to its default — including auth,\n")
	b.WriteString("  which may come back on with a freshly generated password below.\n")
	b.WriteString("  If you rely on one of these, re-export it and re-run this command.\n")
	b.WriteString("================================================================================\n")
	fmt.Fprint(out, b.String())
}

// ensureServicePasswordFallback generates and persists a Web UI password when
// auth is on and the operator did not supply RUNWISP_PASSWORD — an
// operator-supplied one already rides into the service via the env drop-in
// above. Reports whether it wrote a fresh password, so the caller knows a
// restart is needed to pick it up.
func ensureServicePasswordFallback(out io.Writer, installer autostart.Installer, opts autostart.InstallOptions) bool {
	noAuth, err := resolveAuthMode()
	if err != nil {
		return false // misconfigured; reported at daemon start
	}
	if noAuth || os.Getenv("RUNWISP_PASSWORD") != "" {
		return false
	}

	pw, err := datadir.GeneratePassword()
	if err != nil {
		fmt.Fprintf(out, "\nNote: could not generate a Web UI password (%v).\n%s\n", err, manualPasswordHint)
		return false
	}

	path, wrote, err := installer.EnsurePasswordDropIn(context.Background(), opts, pw)
	if err != nil {
		fmt.Fprintf(out, "\nNote: could not set a Web UI password automatically (%v).\n%s\n", err, manualPasswordHint)
		return false
	}
	if !wrote {
		// path == "" means the OS has no drop-in mechanism (launchd); a non-empty
		// path means one already exists and must not be rotated.
		if path == "" {
			fmt.Fprintf(out, "\n%s\n", manualPasswordHint)
		}
		return false
	}

	fmt.Fprintf(out, "\nGenerated a Web UI password and saved it to %s (0600):\n\n    %s\n\n"+
		"Store it now. It won't be shown again here, and 'runwisp password' won't disclose it —\n"+
		"it lives only in that root-owned drop-in. To rotate it, edit or delete the file and restart.\n", path, pw)
	return true
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
	if noAuth, _ := resolveAuthMode(); !noAuth {
		if installer.SupportsPasswordDropIn() {
			action := "Generate a stable Web UI password"
			if os.Getenv("RUNWISP_PASSWORD") != "" {
				action = "Persist your RUNWISP_PASSWORD"
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"  - %s into a 0600 drop-in beside the unit (skipped if one exists)\n", action)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", manualPasswordHint)
		}
	}
	if installer.SupportsPasswordDropIn() && len(capturedServiceEnv()) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(),
			"  - Save your RUNWISP_* environment into a 0600 drop-in beside the unit (refreshed every install)")
	}

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
	opts := autostart.ResolveBinaryOptions{HomeDir: deps.Home}
	exe := override
	if exe == "" {
		var err error
		exe, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate runwisp binary: %w", err)
		}
		// Auto-detected: the binary is the running process, so it exists on
		// disk here. Resolve symlinks and require a regular executable so we
		// never bake an unresolvable or non-executable path into a root
		// ExecStart. An explicit --binary override skips these local-FS checks
		// on purpose — it commonly names a path that only exists on the deploy
		// target (Ansible/Nix). Injection is still blocked unconditionally by
		// the template's control-char rejection and escaping.
		opts.EvalSymlinks = filepath.EvalSymlinks
		opts.Stat = os.Stat
	}
	opts.ExecutablePath = exe
	binary, warning, err := autostart.ResolveBinary(opts)
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
	return &userFacingError{
		title: fmt.Sprintf("a RunWisp daemon started by hand (pid %d) is holding %s:%d", info.Pid, displayHost(host), port),
		details: fmt.Sprintf(
			"It owns the data dir this service would own (%s), so the unit could not start while it runs.\n\n"+
				"  1. Stop it:  runwisp stop --data %s\n"+
				"  2. Re-run this command — the service starts the daemon back up\n\n"+
				"Nothing has been written, and cron has not been touched.",
			info.DataDir, info.DataDir),
	}
}

// printDryRun emits a plan summary for --dry-run.
func printDryRun(w io.Writer, plan autostart.Plan) {
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
