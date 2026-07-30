// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newInstallTestCmd builds a cobra command whose stdout/stderr are captured
// in the provided buffers. It mirrors what the real `service install` command
// receives at runtime.
func newInstallTestCmd(stdout, stderr *bytes.Buffer) *cobra.Command {
	c := &cobra.Command{Use: "install"}
	c.SetOut(stdout)
	c.SetErr(stderr)
	return c
}

func TestFileExists_EmptyPath(t *testing.T) {
	assert.False(t, fileExists(""))
}

func TestPreflightDaemon_PortFree(t *testing.T) {
	t.Parallel()
	// Pick a port that's almost certainly free.
	opts := autostart.InstallOptions{Port: 0}
	require.NoError(t, preflightDaemon(opts, Flags{Host: "127.0.0.1"}))
}

func TestFileExists_MissingPath(t *testing.T) {
	assert.False(t, fileExists(filepath.Join(t.TempDir(), "nope")))
}

func TestFileExists_DirReturnsFalse(t *testing.T) {
	// fileExists must reject directories — install code only cares about
	// regular files like runwisp.toml.
	dir := t.TempDir()
	assert.False(t, fileExists(dir))
}

func TestFileExists_RegularFileReturnsTrue(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.toml")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))
	assert.True(t, fileExists(file))
}

func TestPrintDryRun_RendersPlanAndSteps(t *testing.T) {
	var buf bytes.Buffer
	plan := autostart.Plan{
		Kind:   autostart.PlanInstall,
		Reason: "no unit yet",
		Steps: []autostart.Step{
			{Description: "write unit file"},
			{Description: "reload systemd"},
		},
	}
	printDryRun(&buf, plan)
	got := buf.String()
	assert.Contains(t, got, "Plan: install")
	assert.Contains(t, got, "Reason: no unit yet")
	assert.Contains(t, got, "1. write unit file")
	assert.Contains(t, got, "2. reload systemd")
	assert.NotContains(t, got, "Diff:")
}

func TestPrintDryRun_IncludesDiffWhenPresent(t *testing.T) {
	var buf bytes.Buffer
	plan := autostart.Plan{
		Kind:   autostart.PlanUpdate,
		Reason: "managed unit differs",
		Steps:  []autostart.Step{{Description: "overwrite unit file"}},
		Diff:   "--- old\n+++ new\n@@ -1 +1 @@\n-x\n+y",
	}
	printDryRun(&buf, plan)
	got := buf.String()
	assert.Contains(t, got, "Plan: update")
	assert.Contains(t, got, "Diff:")
	assert.Contains(t, got, "+++ new")
}

func TestResolveDataDirInteractive_Accept(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionAccept,
		Path:   "/data/runwisp",
	})
	require.NoError(t, err)
	assert.Equal(t, "/data/runwisp", path)
}

func TestResolveDataDirInteractive_WarnPrintsAndAccepts(t *testing.T) {
	var stderr bytes.Buffer
	cmd := newInstallTestCmd(&bytes.Buffer{}, &stderr)
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionWarn,
		Detail: "data dir is on a tmpfs",
		Path:   "/tmp/data",
	})
	require.NoError(t, err)
	assert.Equal(t, "/tmp/data", path)
	assert.True(t, strings.Contains(stderr.String(), "data dir is on a tmpfs"))
}

func TestResolveDataDirInteractive_RejectReturnsUserFacing(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	_, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionReject,
		Detail: "HOME and XDG_DATA_HOME both unset",
	})
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Equal(t, "HOME and XDG_DATA_HOME both unset", ufe.title)
}

func TestResolveDataDirInteractive_PromptYes(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{YesNo: []bool{true}}}

	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionPrompt,
		Detail: "use /home/x/.local/share/runwisp?",
		Path:   "/home/x/.local/share/runwisp",
	})
	require.NoError(t, err)
	assert.Equal(t, "/home/x/.local/share/runwisp", path)
}

func TestResolveDataDirInteractive_NoticePrintsAndAccepts(t *testing.T) {
	var stderr bytes.Buffer
	cmd := newInstallTestCmd(&bytes.Buffer{}, &stderr)
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionNotice,
		Detail: `Using data dir /home/x/runwisp (resolved from ".").`,
		Path:   "/home/x/runwisp",
	})
	require.NoError(t, err)
	assert.Equal(t, "/home/x/runwisp", path)
	assert.Contains(t, stderr.String(), "resolved from")
	// A notice is informational — it must not shout "Warning:".
	assert.NotContains(t, stderr.String(), "Warning:")
}

// When the suggested location is declined, the operator is offered the current
// directory; accepting it returns the absolute cwd.
func TestResolveDataDirInteractive_PromptNoThenCurrentDirYes(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{YesNo: []bool{false, true}}}

	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionPrompt,
		Detail: "use default?",
		Path:   "/x",
	})
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(path), "current-dir choice must be absolute: %q", path)
	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, wd, path)
}

// Declining both the suggested location and the current directory dead-ends in a
// user-facing error that points at both frictionless ways to pin a data dir.
func TestResolveDataDirInteractive_PromptNoThenCurrentDirNo(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{YesNo: []bool{false, false}}}

	_, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionPrompt,
		Detail: "use default?",
		Path:   "/x",
	})
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Equal(t, "data dir choice declined", ufe.title)
	assert.Contains(t, ufe.details, "--data .")
}

func TestResolveDataDirInteractive_PromptErrorPropagates(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	// Empty queue → prompter returns an error rather than (false, nil).
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	_, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionPrompt,
		Detail: "use default?",
		Path:   "/x",
	})
	require.Error(t, err)
	// Must NOT be wrapped as a userFacingError — it's a plumbing error.
	var ufe *userFacingError
	assert.False(t, errors.As(err, &ufe), "prompter errors must propagate raw, not wrapped")
}

// requireUnprivileged skips a test that installs with --local when the
// suite happens to run as root (a bare container, typically): --local is
// refused there because systemctl --user has no bus for root.
func requireUnprivileged(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 && runtime.GOOS == "linux" {
		t.Skip("--local is refused as root")
	}
}

// restoreInstallOpts snapshots serviceInstallOpts so individual tests can flip
// the flags without bleeding into siblings.
func restoreInstallOpts(t *testing.T) {
	t.Helper()
	orig := serviceInstallOpts
	t.Cleanup(func() { serviceInstallOpts = orig })
}

// durableTempDir creates a temp dir under the working tree so paths it produces
// don't get rejected by autostart.ResolveBinary's /tmp/durability check.
func durableTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "svc-install-test-*")
	require.NoError(t, err)
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(abs) })
	return abs
}

// installFlagsCmd wires a cobra command with --config / --data flags and
// stdout/stderr captured into the supplied buffers.
func installFlagsCmd(t *testing.T, f Flags, stdout, stderr *bytes.Buffer) *cobra.Command {
	t.Helper()
	c := newInstallTestCmd(stdout, stderr)
	c.Flags().String("config", f.CfgFile, "")
	c.Flags().String("data", f.DataDir, "")
	return c
}

func TestResolveServiceOptions_BuildsAbsolutePaths(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	// Use an explicit binary override so we don't hit the "test binary in
	// /tmp/go-build" rejection path.
	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	f := Flags{CfgFile: tomlPath, DataDir: dir}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "tester",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	opts, err := resolveServiceOptions(cmd, deps, f, false)
	require.NoError(t, err)
	assert.Equal(t, binary, opts.Binary)
	assert.Equal(t, tomlPath, opts.Config)
	// DataDir must be absolutized; it should at minimum begin with /.
	assert.True(t, strings.HasPrefix(opts.DataDir, "/"), "data dir must be absolute: %q", opts.DataDir)
}

func TestResolveServiceOptions_BinaryOverrideIsUsed(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	binary := filepath.Join(dir, "bin", "runwisp")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0o755))
	require.NoError(t, os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755))

	serviceInstallOpts.Binary = binary

	cfgFile := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgFile, []byte(""), 0o600))
	f := Flags{CfgFile: cfgFile, DataDir: dir}

	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", cfgFile))

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "tester",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	opts, err := resolveServiceOptions(cmd, deps, f, false)
	require.NoError(t, err)
	assert.Equal(t, binary, opts.Binary, "binary override must be honoured")
}

// TestResolveServiceOptions_SystemUsesEuidDefaultsWhenNotExplicit guards the
// bug where a system install with no explicit --config/--data would route
// through the interactive XDG resolution (~/.config/runwisp/runwisp.toml)
// instead of the euid-derived root default (/etc/runwisp/runwisp.toml)
// resolvePathDefaults already put in f.CfgFile/f.DataDir — so
// `sudo runwisp service install` failed preflight even though the config
// exists right where root's own default put it.
func TestResolveServiceOptions_SystemUsesEuidDefaultsWhenNotExplicit(t *testing.T) {
	restoreInstallOpts(t)

	// durableTempDir, not t.TempDir: ResolveBinary refuses a binary under
	// /tmp, which is exactly where TMPDIR points on Linux.
	binary := filepath.Join(durableTempDir(t), "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	// f already holds what resolvePathDefaults(0) would have filled in — the
	// point under test is that resolveServiceOptions passes it through
	// rather than consulting XDG/bare-cwd (deps.Home here is a decoy: if the
	// XDG path were consulted, opts.Config would end up under it instead).
	f := Flags{CfgFile: "/etc/runwisp/runwisp.toml", DataDir: "/var/lib/runwisp"}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	// Deliberately not calling cmd.Flags().Set — neither flag was passed.

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "root",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	opts, err := resolveServiceOptions(cmd, deps, f, true)
	require.NoError(t, err)
	assert.Equal(t, "/etc/runwisp/runwisp.toml", opts.Config)
	assert.Equal(t, "/var/lib/runwisp", opts.DataDir)
	assert.True(t, opts.System, "system scope must be recorded on the options")
}

// TestResolveServiceOptions_SystemStillHonorsExplicitFlags guards the other
// direction: an explicit --config/--data on a system install must still go
// through the normal validated path (so e.g. a relative --data still gets
// absolutized), not the euid-default bypass.
func TestResolveServiceOptions_SystemStillHonorsExplicitFlags(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	f := Flags{CfgFile: tomlPath, DataDir: dir}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "root",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	opts, err := resolveServiceOptions(cmd, deps, f, true)
	require.NoError(t, err)
	assert.Equal(t, tomlPath, opts.Config)
	assert.Equal(t, dir, opts.DataDir)
}

// TestResolveServiceOptions_SystemRejectsUntrustedConfig closes the hole a
// system install otherwise leaves open: `sudo runwisp service install
// --config <path>` bakes whatever that path resolves to into a root-owned
// unit, so the file needs the same ownership guarantee a cron source
// already gets before anything is written.
func TestResolveServiceOptions_SystemRejectsUntrustedConfig(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))
	require.NoError(t, os.Chmod(tomlPath, 0o666)) // force world-writable past umask

	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	f := Flags{CfgFile: tomlPath, DataDir: dir}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "root",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	_, err := resolveServiceOptions(cmd, deps, f, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "world-writable")
}

// TestResolveServiceOptions_NonSystemAllowsUntrustedConfig: the trust check
// is specific to the system scope, since a --local install only ever runs
// with the operator's own privilege — there is no escalation to guard
// against.
func TestResolveServiceOptions_NonSystemAllowsUntrustedConfig(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))
	require.NoError(t, os.Chmod(tomlPath, 0o666))

	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	f := Flags{CfgFile: tomlPath, DataDir: dir}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	deps := autostart.Deps{
		Home:        t.TempDir(),
		User:        "alice",
		Fingerprint: "fp-test",
		Prompter:    &autostart.ScriptedPrompter{},
	}
	opts, err := resolveServiceOptions(cmd, deps, f, false)
	require.NoError(t, err)
	assert.Equal(t, tomlPath, opts.Config)
}

func TestRunServiceInstall_PrintRendersUnit(t *testing.T) {
	// service install picks the platform's autostart backend: systemd on
	// Linux, launchd on macOS, scm on Windows. The [Unit]/[Service] section
	// names below are systemd-specific.
	if runtime.GOOS != "linux" {
		t.Skip("systemd unit shape is Linux-only")
	}
	restoreInstallOpts(t)

	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	f := Flags{CfgFile: tomlPath, DataDir: dir, Host: "127.0.0.1", Port: 9477}

	// Force --print so install path skips disk I/O.
	serviceInstallOpts.Print = true
	// The tests run unprivileged, so the default (system) scope would refuse.
	requireUnprivileged(t)
	serviceInstallOpts.Local = true
	// Point the binary override at a real file so ResolveBinary succeeds.
	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	stdout := &bytes.Buffer{}
	cmd := installFlagsCmd(t, f, stdout, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	err := runServiceInstall(cmd, f)
	require.NoError(t, err)
	out := stdout.String()
	// systemd unit always carries [Unit] and [Service] sections.
	assert.Contains(t, out, "[Unit]")
	assert.Contains(t, out, "[Service]")
}

func TestRunServiceInstall_DryRunPrintsPlan(t *testing.T) {
	restoreInstallOpts(t)

	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	f := Flags{CfgFile: tomlPath, DataDir: dir, Host: "127.0.0.1", Port: 9477}

	serviceInstallOpts.DryRun = true
	requireUnprivileged(t)
	serviceInstallOpts.Local = true
	binary := filepath.Join(dir, "runwisp-binary")
	require.NoError(t, os.WriteFile(binary, []byte("\x7fELF"), 0o755))
	serviceInstallOpts.Binary = binary

	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	stdout := &bytes.Buffer{}
	cmd := installFlagsCmd(t, f, stdout, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))

	err := runServiceInstall(cmd, f)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "Plan:")
}

func TestRunServiceUninstall_NoUnitExists(t *testing.T) {
	// Snapshot uninstall opts.
	origOpts := serviceUninstallOpts
	t.Cleanup(func() { serviceUninstallOpts = origOpts })
	serviceUninstallOpts.Yes = true

	dir := durableTempDir(t)
	f := Flags{DataDir: dir}

	// Point HOME at an empty temp dir so the systemd installer finds no unit.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "share"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	stdout := &bytes.Buffer{}
	cmd := newInstallTestCmd(stdout, &bytes.Buffer{})

	// Uninstall against an empty home is a no-op success on the systemd path.
	require.NoError(t, runServiceUninstall(cmd, f))
}

// writeGateTestConfig writes a minimal runwisp.toml with include_cron
// pointed at a one-line cron.d file, and returns the config path.
func writeGateTestConfig(t *testing.T, cronLine string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "cron.d"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cron.d", "backup"), []byte(cronLine), 0o600))
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath,
		[]byte("[daemon]\ninclude_cron = [\"cron.d/*\"]\n"), 0o600))
	return tomlPath
}

func TestEvaluateCronTakeover_RequiresSystemAndRoot(t *testing.T) {
	_, refusal := evaluateCronTakeover("", false, 0, false)
	require.NotNil(t, refusal)
	assert.Contains(t, refusal.title, "requires the system service and root")

	_, refusal = evaluateCronTakeover("", true, 1000, false)
	require.NotNil(t, refusal)
	assert.Contains(t, refusal.title, "requires the system service and root")
}

func TestEvaluateCronTakeover_RefusesWhenNothingIsBeingRead(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	_, refusal := evaluateCronTakeover(tomlPath, true, 0, false)
	require.NotNil(t, refusal)
	assert.Contains(t, refusal.title, "no cron jobs are being read")
}

func TestEvaluateCronTakeover_RefusesUnreadableConfig(t *testing.T) {
	_, refusal := evaluateCronTakeover(filepath.Join(t.TempDir(), "absent.toml"), true, 0, false)
	require.NotNil(t, refusal)
	assert.Contains(t, refusal.title, "cannot take over cron")
}

func TestEvaluateCronTakeover_RefusesSkippedJobsUnlessAllowed(t *testing.T) {
	// A line with no user column in a cron.d file is structurally invalid —
	// it produces a CronFinding{Skipped: true}, not a load error.
	tomlPath := writeGateTestConfig(t, "* * * * * echo \"no user column\"\n")

	_, refusal := evaluateCronTakeover(tomlPath, true, 0, false)
	require.NotNil(t, refusal)
	assert.Contains(t, refusal.title, "cron job(s) failed to load")

	cfg, refusal := evaluateCronTakeover(tomlPath, true, 0, true)
	assert.Nil(t, refusal)
	assert.NotNil(t, cfg)
}

// TestEvaluateCronTakeover_AllowsCleanConfigAsRoot also stands in for
// refusal #4 (run-as-user): refusal #1 already requires euid 0 to reach
// this point, and a system unit always runs the daemon as root with no
// privilege-drop option today, so config.RunUserFindings(cfg, 0) is always
// empty here — refusal #4 cannot fire through this function as currently
// wired. It stays as defense-in-depth (see the comment there) and is
// exercised directly, with a non-root euid, by the RunUserFindings tests in
// internal/config/cronsource_test.go.
func TestEvaluateCronTakeover_AllowsCleanConfigAsRoot(t *testing.T) {
	tomlPath := writeGateTestConfig(t, "0 3 * * * root /usr/local/bin/backup.sh\n")

	cfg, refusal := evaluateCronTakeover(tomlPath, true, 0, false)
	require.Nil(t, refusal)
	require.NotNil(t, cfg)
	assert.Len(t, cfg.CronFiles(), 1)
}

// fakeCronInstaller stubs the one Installer method offerCronTakeover uses.
// Everything else panics: reaching it would mean the prompt path grew a
// dependency this test is not describing.
type fakeCronInstaller struct {
	autostart.Installer
	unit   string
	active bool
	err    error
}

func (f fakeCronInstaller) CronStatus(context.Context) (string, bool, error) {
	return f.unit, f.active, f.err
}

// offerTakeoverFor runs the prompt path against a clean take-over-able
// config and reports whether it ended up setting TakeOverCron.
func offerTakeoverFor(t *testing.T, deps autostart.Deps, installer autostart.Installer, systemWide bool) (bool, string) {
	t.Helper()
	tomlPath := writeGateTestConfig(t, "0 3 * * * root /usr/local/bin/backup.sh\n")
	opts := autostart.InstallOptions{Config: tomlPath, System: systemWide}
	var stderr bytes.Buffer
	cmd := newInstallTestCmd(&bytes.Buffer{}, &stderr)
	require.NoError(t, offerCronTakeover(cmd, deps, installer, &opts))
	return opts.TakeOverCron, stderr.String()
}

// promptDeps is a Deps that would let the cron prompt through, answering it
// with the supplied yes/no.
func promptDeps(answer bool) autostart.Deps {
	return autostart.Deps{
		Euid:       0,
		StdinIsTTY: true,
		Prompter:   &autostart.ScriptedPrompter{YesNo: []bool{answer}},
	}
}

func TestOfferCronTakeover_AcceptedSetsTakeOverCron(t *testing.T) {
	restoreInstallOpts(t)
	took, _ := offerTakeoverFor(t, promptDeps(true), fakeCronInstaller{unit: "cron.service", active: true}, true)
	assert.True(t, took)
}

func TestOfferCronTakeover_DeclinedLeavesCronAlone(t *testing.T) {
	restoreInstallOpts(t)
	took, _ := offerTakeoverFor(t, promptDeps(false), fakeCronInstaller{unit: "cron.service", active: true}, true)
	assert.False(t, took)
}

// The prompt is only ever asked when the answer could be acted on. Each of
// these would otherwise be a question whose "yes" we could not honour — or,
// for --yes, a destructive default nobody typed.
func TestOfferCronTakeover_SkippedWhenNotAskable(t *testing.T) {
	restoreInstallOpts(t)
	live := fakeCronInstaller{unit: "cron.service", active: true}

	cases := []struct {
		name       string
		deps       autostart.Deps
		installer  autostart.Installer
		systemWide bool
	}{
		{"user scope", promptDeps(true), live, false},
		{"not root", autostart.Deps{Euid: 1000, StdinIsTTY: true, Prompter: &autostart.ScriptedPrompter{}}, live, true},
		{"no tty", autostart.Deps{Euid: 0, Prompter: &autostart.ScriptedPrompter{}}, live, true},
		{"--yes", autostart.Deps{Euid: 0, StdinIsTTY: true, AutoOK: true, Prompter: &autostart.ScriptedPrompter{}}, live, true},
		{"no cron unit", promptDeps(true), fakeCronInstaller{}, true},
		{"cron not running", promptDeps(true), fakeCronInstaller{unit: "cron.service"}, true},
		{"probe failed", promptDeps(true), fakeCronInstaller{err: errors.New("systemctl exploded")}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A ScriptedPrompter with no queued answer errors if consulted,
			// so "no prompt" is enforced, not merely assumed.
			took, _ := offerTakeoverFor(t, tc.deps, tc.installer, tc.systemWide)
			assert.False(t, took)
		})
	}
}

// A refusal on a host where cron IS running is worth a word: the operator
// is about to end up with two schedulers and should know why RunWisp isn't
// offering to fix it.
func TestOfferCronTakeover_NotesRefusalWhenCronIsRunning(t *testing.T) {
	restoreInstallOpts(t)
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))

	opts := autostart.InstallOptions{Config: tomlPath, System: true}
	var stderr bytes.Buffer
	cmd := newInstallTestCmd(&bytes.Buffer{}, &stderr)
	deps := autostart.Deps{Euid: 0, StdinIsTTY: true, Prompter: &autostart.ScriptedPrompter{}}

	require.NoError(t, offerCronTakeover(cmd, deps, fakeCronInstaller{unit: "cron.service", active: true}, &opts))
	assert.False(t, opts.TakeOverCron)
	assert.Contains(t, stderr.String(), "cron.service is running")
	assert.Contains(t, stderr.String(), "no cron jobs are being read")
}

func TestResolveDataDirInteractive_UnknownActionFallsThrough(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{}}

	// Garbage action value falls through to the default return — returns the
	// supplied path with no error.
	path, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveAction(99),
		Path:   "/fallthrough",
	})
	require.NoError(t, err)
	assert.Equal(t, "/fallthrough", path)
}
