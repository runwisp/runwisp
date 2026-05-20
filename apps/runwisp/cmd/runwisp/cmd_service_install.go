// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/runwisp/runwisp/internal/autostart"
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
	System bool
	// Binary overrides the auto-detected binary path baked into the
	// unit. Useful for Ansible/Nix where the running binary is not
	// the one that will end up on disk.
	Binary string
}

var serviceInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Wire up systemd or launchd so the daemon starts on boot",
	Long: `Wire RunWisp into the host init system.

On Linux/WSL this writes ~/.config/systemd/user/runwisp.service and enables
linger so the daemon survives logout. On macOS it writes
~/Library/LaunchAgents/com.runwisp.daemon.plist. No root required for the
default user-scoped install.

Re-running is idempotent: a matching unit is a no-op, a drifted unit
prompts before overwrite, a hand-edited unit refuses without --force.

Flags:
  --yes        skip confirmation (CI-safe; not allowed for --purge)
  --print      write the rendered unit to stdout and exit
  --dry-run    print the plan and exit without writing anything
  --force      overwrite a hand-edited unit
  --system     install /etc/systemd/system/ instead (Linux, advanced)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServiceInstall(cmd)
	},
}

func init() {
	serviceInstallCmd.Flags().BoolVarP(&serviceInstallOpts.Yes, "yes", "y", false, "skip confirmation prompts")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.Print, "print", false, "print the rendered unit to stdout and exit")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.DryRun, "dry-run", false, "print the plan and exit without writing")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.Force, "force", false, "overwrite a hand-edited unit")
	serviceInstallCmd.Flags().BoolVar(&serviceInstallOpts.System, "system", false, "install a system-wide unit (Linux, advanced)")
	serviceInstallCmd.Flags().StringVar(&serviceInstallOpts.Binary, "binary", "", "override the binary path baked into the unit (default: auto-detect)")
}

func runServiceInstall(cmd *cobra.Command) error {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, serviceInstallOpts.Yes)
	if err != nil {
		return err
	}

	opts, err := resolveServiceOptions(cmd, deps)
	if err != nil {
		return err
	}
	opts.System = serviceInstallOpts.System
	opts.Force = serviceInstallOpts.Force

	installer, err := autostart.New(deps)
	if err != nil {
		return err
	}

	if serviceInstallOpts.Print {
		body, err := installer.Render(opts)
		if err != nil {
			return err
		}
		_, err = cmd.OutOrStdout().Write(body)
		return err
	}

	if serviceInstallOpts.DryRun {
		plan, err := installer.ComputePlan(context.Background(), opts)
		if err != nil {
			return err
		}
		printDryRun(cmd.OutOrStdout(), plan)
		return nil
	}

	if err := preflightDaemon(opts); err != nil {
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

// resolveServiceOptions turns the global --config / --data flags plus
// os.Executable() into an autostart.InstallOptions. Prompts the
// operator when the data dir / config path is ambiguous (default
// "./data" with no DB, the bare ./runwisp.toml shadowing the XDG one,
// etc.). The returned options are fully absolute — what we'd bake
// into the unit.
func resolveServiceOptions(cmd *cobra.Command, deps autostart.Deps) (autostart.InstallOptions, error) {
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

	dataDirFlag := cmd.Flag("data")
	dataDirExplicit := dataDirFlag != nil && dataDirFlag.Changed
	bareDBExists := false
	if _, err := os.Stat(filepath.Join("data", "runwisp.db")); err == nil {
		bareDBExists = true
	}
	dataRes, err := autostart.ResolveDataDir(autostart.ResolveDataDirOptions{
		Explicit:         flags.DataDir,
		ExplicitSet:      dataDirExplicit,
		HomeDir:          deps.Home,
		XDGDataHome:      deps.XDGDataHome,
		BareDefaultHasDB: bareDBExists,
	})
	if err != nil {
		return autostart.InstallOptions{}, err
	}
	dataDir, err := resolveDataDirInteractive(cmd, deps, dataRes)
	if err != nil {
		return autostart.InstallOptions{}, err
	}

	cfgFlag := cmd.Flag("config")
	cfgExplicit := cfgFlag != nil && cfgFlag.Changed
	xdgCfg := autostart.XDGConfigPath(deps.Home, deps.XDGConfHome)
	xdgExists := xdgCfg != "" && fileExists(xdgCfg)
	bareCfgExists := fileExists("runwisp.toml")
	configPath, err := autostart.ResolveConfigPath(autostart.ResolveConfigOptions{
		Explicit:    flags.CfgFile,
		ExplicitSet: cfgExplicit,
		HomeDir:     deps.Home,
		XDGConfHome: deps.XDGConfHome,
		XDGExists:   xdgExists,
		BareExists:  bareCfgExists,
	})
	if err != nil {
		return autostart.InstallOptions{}, err
	}

	return autostart.InstallOptions{
		Binary:  binary,
		Config:  configPath,
		DataDir: dataDir,
		Host:    flags.Host,
		Port:    flags.Port,
	}, nil
}

// resolveDataDirInteractive folds in operator confirmation when
// ResolveDataDir asked for one. Returns the final absolute path.
func resolveDataDirInteractive(cmd *cobra.Command, deps autostart.Deps, res autostart.ResolveDataDirResult) (string, error) {
	switch res.Action {
	case autostart.ResolveActionAccept:
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
		if !ok {
			return "", &userFacingError{
				title:   "data dir choice declined",
				details: "Re-run with --data <absolute-path> to pin a location.",
			}
		}
		return res.Path, nil
	}
	return res.Path, nil
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
func preflightDaemon(opts autostart.InstallOptions) error {
	if bindErr := probePortAvailable(flags.Host, opts.Port); bindErr != nil {
		return portConflictError(flags.Host, opts.Port, bindErr)
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
