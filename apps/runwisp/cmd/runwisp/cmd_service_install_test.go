// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
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

func TestResolveDataDirInteractive_PromptNoReturnsUserFacing(t *testing.T) {
	cmd := newInstallTestCmd(&bytes.Buffer{}, &bytes.Buffer{})
	deps := autostart.Deps{Prompter: &autostart.ScriptedPrompter{YesNo: []bool{false}}}

	_, err := resolveDataDirInteractive(cmd, deps, autostart.ResolveDataDirResult{
		Action: autostart.ResolveActionPrompt,
		Detail: "use default?",
		Path:   "/x",
	})
	require.Error(t, err)
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Equal(t, "data dir choice declined", ufe.title)
	assert.Contains(t, ufe.details, "--data")
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
	opts, err := resolveServiceOptions(cmd, deps, f)
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
	opts, err := resolveServiceOptions(cmd, deps, f)
	require.NoError(t, err)
	assert.Equal(t, binary, opts.Binary, "binary override must be honoured")
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
