// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
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
	// Port 0 always binds, so nothing else is consulted — hence the nil
	// installer, which would panic if the probe path were reached.
	opts := autostart.InstallOptions{Port: 0}
	stale, err := preflightDaemon(context.Background(), nil, opts, Flags{Host: "127.0.0.1"})
	require.NoError(t, err)
	assert.False(t, stale)
}

// statusInstaller stubs the one Installer method the port preflight uses.
// Everything else panics: reaching it would mean the preflight grew a
// dependency this test is not describing.
type statusInstaller struct {
	autostart.Installer
	st  autostart.Status
	err error
}

func (s statusInstaller) Status(context.Context, autostart.InstallOptions) (autostart.Status, error) {
	return s.st, s.err
}

func (s statusInstaller) SupportsPasswordDropIn() bool { return true }

// noStatusInstaller fails the test if the unit is probed at all — the answer for
// a port holder that isn't ours can't depend on it, and asking costs systemctl
// round-trips.
type noStatusInstaller struct{ autostart.Installer }

func (noStatusInstaller) Status(context.Context, autostart.InstallOptions) (autostart.Status, error) {
	panic("unit state must not be probed for a port holder that is not ours")
}

// runwispOnPort stands in for a live daemon holding a port: a real listener on
// loopback answering the one public identity endpoint probeRunwispInstance asks
// for. Returns the port it took.
func runwispOnPort(t *testing.T, info model.InstanceInfo) int {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon/identity", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(info)
	})
	return servePort(t, mux)
}

// servePort runs h on a loopback port and reports which one, so a test can
// describe "something is already listening here".
func servePort(t *testing.T, h http.Handler) int {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	return port
}

// The regression that made `runwisp takeover` impossible on the boxes it exists
// for: RunWisp already running as this very service holds the port, so the
// install refused before it ever masked cron — and the reload that lifts the
// hold was unreachable.
func TestPreflightDaemon_OwnServiceHoldingThePortIsNotAConflict(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 4242, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}
	installer := statusInstaller{st: autostart.Status{
		Installed: true, Running: true,
		UnitConfigHash: "same", ExpectedConfigHash: "same",
	}}

	stale, err := preflightDaemon(context.Background(), installer, opts, Flags{Host: "127.0.0.1"})
	require.NoError(t, err)
	assert.False(t, stale, "an unchanged unit leaves the running daemon current")
}

// systemd keeps a running unit on the settings it started with, so an install
// that re-describes a live service with a different port or data dir has to say
// so — otherwise "Installed and started" reads as "serving on the new port".
func TestPreflightDaemon_ReportsStaleSettingsForRunningService(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 4242, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}
	installer := statusInstaller{st: autostart.Status{
		Installed: true, Running: true,
		UnitConfigHash: "old", ExpectedConfigHash: "new",
	}}

	stale, err := preflightDaemon(context.Background(), installer, opts, Flags{Host: "127.0.0.1"})
	require.NoError(t, err)
	assert.True(t, stale)
}

// Same daemon, but nothing started it for us: the unit we are about to enable
// would lose the race for the data dir, so this one still refuses — with the
// message that actually helps, not "run on a different port".
func TestPreflightDaemon_HandStartedDaemonRefusesWithStopHint(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 4242, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}
	installer := statusInstaller{st: autostart.Status{Installed: true}}

	_, err := preflightDaemon(context.Background(), installer, opts, Flags{Host: "127.0.0.1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "started by hand")
	assert.Contains(t, err.Error(), "runwisp stop --data "+dataDir)
	assert.NotContains(t, err.Error(), "different port", "two daemons on one database is not a port problem")
}

// A failed unit probe must land here too, not on the "it's our service" side:
// guessing that way would wave the install through into a port fight.
func TestPreflightDaemon_UnknownUnitStateRefuses(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 7, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}
	installer := statusInstaller{err: errors.New("systemctl exploded")}

	_, err := preflightDaemon(context.Background(), installer, opts, Flags{Host: "127.0.0.1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "started by hand")
}

func TestPreflightDaemon_ForeignPortHolderStillRefuses(t *testing.T) {
	port := servePort(t, http.NotFoundHandler())
	opts := autostart.InstallOptions{Port: port, DataDir: t.TempDir(), System: true}

	_, err := preflightDaemon(context.Background(), noStatusInstaller{}, opts, Flags{Host: "127.0.0.1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use by another process")
}

// A RunWisp daemon on someone else's data dir is as much of a conflict as a
// stranger's web server, and answering that costs no systemctl call.
func TestPreflightDaemon_RunwispOnAnotherDataDirRefuses(t *testing.T) {
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 99, DataDir: "/somewhere/else"})
	opts := autostart.InstallOptions{Port: port, DataDir: t.TempDir(), System: true}

	_, err := preflightDaemon(context.Background(), noStatusInstaller{}, opts, Flags{Host: "127.0.0.1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another RunWisp daemon")
}

// planInstaller stubs the two Installer methods a --dry-run reaches: the plan it
// prints, and the unit state the port preflight consults.
type planInstaller struct {
	statusInstaller
	plan autostart.Plan
}

func (p planInstaller) ComputePlan(context.Context, autostart.InstallOptions) (autostart.Plan, error) {
	return p.plan, nil
}

// A dry run used to return before the port preflight, so it printed a clean plan
// for an install that would stop before writing anything — and the case it hid is
// exactly the one `runwisp takeover` has a recovery path for.
func TestInspectServiceInstall_DryRunSurfacesAHandStartedDaemon(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 4242, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}
	installer := planInstaller{
		statusInstaller: statusInstaller{st: autostart.Status{Installed: true}},
		plan:            autostart.Plan{Kind: autostart.PlanInstall, Reason: "no unit yet"},
	}

	stdout := &bytes.Buffer{}
	err := inspectServiceInstall(newInstallTestCmd(stdout, &bytes.Buffer{}), installer, opts,
		installRequest{DryRun: true}, Flags{Host: "127.0.0.1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "started by hand")
	assert.Contains(t, stdout.String(), "Plan:", "the plan still prints — the conflict is extra, not instead")
}

// renderInstaller answers --print and nothing else: any other call lands on the
// nil embedded Installer and panics, which is the assertion.
type renderInstaller struct {
	autostart.Installer
	body []byte
}

func (r renderInstaller) Render(autostart.InstallOptions) ([]byte, error) { return r.body, nil }

// --print stays byte-clean for piping into a unit file, so it must not reach the
// preflight: a note (or a refusal) in the middle of the output would corrupt it.
func TestInspectServiceInstall_PrintNeverConsultsThePort(t *testing.T) {
	dataDir := t.TempDir()
	port := runwispOnPort(t, model.InstanceInfo{App: server.AppName, Pid: 4242, DataDir: dataDir})
	opts := autostart.InstallOptions{Port: port, DataDir: dataDir, System: true}

	stdout := &bytes.Buffer{}
	err := inspectServiceInstall(newInstallTestCmd(stdout, &bytes.Buffer{}),
		renderInstaller{body: []byte("[Unit]\n")}, opts,
		installRequest{Print: true}, Flags{Host: "127.0.0.1"})

	require.NoError(t, err)
	assert.Equal(t, "[Unit]\n", stdout.String())
}

// noDropInInstaller answers --dry-run's plan and reports no password drop-in
// support (the launchd/unsupported-OS case) — anything else panics on the nil
// embedded Installer.
type noDropInInstaller struct {
	autostart.Installer
	plan autostart.Plan
}

func (n noDropInInstaller) ComputePlan(context.Context, autostart.InstallOptions) (autostart.Plan, error) {
	return n.plan, nil
}

func (n noDropInInstaller) SupportsPasswordDropIn() bool { return false }

// A dry run used to always describe a password drop-in whenever auth was on,
// even on an OS (launchd, or one with no installer at all) where the real
// install falls back to the manual hint instead. The dry run must match what
// the real install actually does.
func TestInspectServiceInstall_DryRunWithoutDropInSupportShowsManualHint(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "")
	t.Setenv("RUNWISP_AUTH", "")
	installer := noDropInInstaller{plan: autostart.Plan{Kind: autostart.PlanInstall, Reason: "no unit yet"}}
	opts := autostart.InstallOptions{Port: 0}

	stdout := &bytes.Buffer{}
	err := inspectServiceInstall(newInstallTestCmd(stdout, &bytes.Buffer{}), installer, opts,
		installRequest{DryRun: true}, Flags{Host: "127.0.0.1"})

	require.NoError(t, err)
	assert.Contains(t, stdout.String(), manualPasswordHint)
	assert.NotContains(t, stdout.String(), "drop-in beside the unit")
}

func TestSamePath(t *testing.T) {
	assert.True(t, samePath("/var/lib/runwisp/", "/var/lib/./runwisp"))
	assert.False(t, samePath("/var/lib/runwisp", "/var/lib/other"))
	assert.False(t, samePath("", "/var/lib/runwisp"), "an unknown path is not the same path")
	assert.False(t, samePath("/var/lib/runwisp", ""))
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
	opts, err := resolveServiceOptions(cmd, deps, f, false, serviceInstallOpts.Binary)
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
	opts, err := resolveServiceOptions(cmd, deps, f, false, serviceInstallOpts.Binary)
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
	opts, err := resolveServiceOptions(cmd, deps, f, true, serviceInstallOpts.Binary)
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
	opts, err := resolveServiceOptions(cmd, deps, f, true, serviceInstallOpts.Binary)
	require.NoError(t, err)
	assert.Equal(t, tomlPath, opts.Config)
	assert.Equal(t, dir, opts.DataDir)
}

// TestAssertTrustedIfSystem_RejectsUntrustedConfig closes the hole a system
// install otherwise leaves open: `sudo runwisp service install --config <path>`
// bakes whatever that path resolves to into a root-owned unit, so the file needs
// the same ownership guarantee a cron source already gets before anything is
// written.
//
// Asserted on the check itself rather than through resolveServiceOptions, because
// `takeover` shares that resolution and reports this condition as a plan blocker
// instead — so a dry run can print it rather than dying before it says anything.
func TestAssertTrustedIfSystem_RejectsUntrustedConfig(t *testing.T) {
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))
	require.NoError(t, os.Chmod(tomlPath, 0o666)) // force world-writable past umask

	_, err := assertTrustedIfSystem(tomlPath, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "world-writable")
}

// The trust check is specific to the system scope: a --local install only ever
// runs with the operator's own privilege, so there is no escalation to guard
// against.
func TestAssertTrustedIfSystem_NonSystemAllowsUntrustedConfig(t *testing.T) {
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))
	require.NoError(t, os.Chmod(tomlPath, 0o666))

	path, err := assertTrustedIfSystem(tomlPath, false)
	require.NoError(t, err)
	assert.Equal(t, tomlPath, path)
}

// A path that does not exist yet is nobody's problem here: there is no owner to
// distrust, and the install refuses a missing config later with a better message.
func TestAssertTrustedIfSystem_MissingConfigPassesThrough(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "runwisp.toml")
	path, err := assertTrustedIfSystem(missing, true)
	require.NoError(t, err)
	assert.Equal(t, missing, path)
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

// pwInstaller is a minimal Installer that records EnsurePasswordDropIn/
// WriteEnvDropIn/Restart so ensureServiceEnv's env-gating and print behavior
// can be asserted without touching a real init system.
type pwInstaller struct {
	autostart.Installer // nil embed: only the methods below are exercised

	path           string
	wrote          bool
	ensureErr      error
	ensureCalled   bool
	ensurePassword string

	envPath   string
	envChange autostart.DropInChange
	envErr    error
	envCalled bool
	envVars   map[string]string

	restarts int
}

func (p *pwInstaller) EnsurePasswordDropIn(_ context.Context, _ autostart.InstallOptions, password string) (string, bool, error) {
	p.ensureCalled = true
	p.ensurePassword = password
	return p.path, p.wrote, p.ensureErr
}

func (p *pwInstaller) WriteEnvDropIn(_ context.Context, _ autostart.InstallOptions, _ string, vars map[string]string) (string, autostart.DropInChange, error) {
	p.envCalled = true
	p.envVars = vars
	return p.envPath, p.envChange, p.envErr
}

func (p *pwInstaller) Restart(context.Context, autostart.InstallOptions) error {
	p.restarts++
	return nil
}

// #251 regression: RUNWISP_AUTH (and any other RUNWISP_*) used to be silently
// dropped on install — a systemd unit / launchd plist never inherits the
// install shell's environment. capturedServiceEnv is what ensureServiceEnv
// feeds into WriteEnvDropIn to carry it into the service instead.
func TestCapturedServiceEnv_CapturesRunwispVarsExcludesMarkerAndOthers(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	t.Setenv("RUNWISP_LOG_LEVEL", "debug")
	t.Setenv(autostart.ServiceManagedEnv, "1")
	t.Setenv("OTHER_VAR", "keep-out")

	got := capturedServiceEnv()

	assert.Equal(t, "off", got["RUNWISP_AUTH"])
	assert.Equal(t, "debug", got["RUNWISP_LOG_LEVEL"])
	_, hasMarker := got[autostart.ServiceManagedEnv]
	assert.False(t, hasMarker, "the service-managed marker is set by the unit itself, not operator input")
	_, hasOther := got["OTHER_VAR"]
	assert.False(t, hasOther, "only RUNWISP_* is captured")
}

// An operator-supplied RUNWISP_PASSWORD at install time used to make
// ensureServicePassword skip entirely — but the install shell's env doesn't
// propagate to the systemd unit, so the service would boot with no password env
// and mint a fresh random one every boot. The fix carries the operator's value
// into the service via the same env drop-in every other RUNWISP_* var rides in
// on, never the generated-password fallback's own drop-in.
func TestEnsureServiceEnv_PersistsOperatorSuppliedPasswordViaEnvDropIn(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "hunter2")
	t.Setenv("RUNWISP_AUTH", "")
	inst := &pwInstaller{envPath: "/etc/systemd/system/runwisp.service.d/runwisp-env.conf", envChange: autostart.DropInWritten}
	var out bytes.Buffer
	ensureServiceEnv(newInstallTestCmd(&out, &bytes.Buffer{}), inst, autostart.InstallOptions{})

	require.True(t, inst.envCalled)
	assert.Equal(t, "hunter2", inst.envVars["RUNWISP_PASSWORD"])
	assert.False(t, inst.ensureCalled, "an operator-supplied password rides the env drop-in, not the generated-password fallback")
	assert.Equal(t, 1, inst.restarts, "must restart so the daemon picks up the new environment")
	assert.NotContains(t, out.String(), "hunter2", "must not echo the operator's secret back")
	assert.Contains(t, out.String(), inst.envPath)
}

// #251: RUNWISP_AUTH=off used to only get a "does not carry" warning. It now
// rides the env drop-in like every other RUNWISP_* var, so the warning (and
// the password fallback, since there's no password boundary to manage) both
// disappear.
func TestEnsureServiceEnv_AuthOffStillCapturedNoPasswordFallback(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	t.Setenv("RUNWISP_PASSWORD", "")
	inst := &pwInstaller{}
	var out bytes.Buffer
	ensureServiceEnv(newInstallTestCmd(&out, &bytes.Buffer{}), inst, autostart.InstallOptions{})

	require.True(t, inst.envCalled)
	assert.Equal(t, "off", inst.envVars["RUNWISP_AUTH"])
	assert.False(t, inst.ensureCalled, "no password boundary means no password to set")
	assert.NotContains(t, out.String(), "does not carry into the managed service",
		"RUNWISP_AUTH now rides the env drop-in, so the old warning is stale")
}

// A reinstall run from a shell that no longer exports the operator's
// RUNWISP_* vars (fresh terminal, CI, upgrade script) makes WriteEnvDropIn
// remove a previous install's captured environment. This must never be
// reported as "Saved" — the environment was deleted, not written — and it
// must warn loudly, since a removed RUNWISP_AUTH=off silently re-enables auth.
func TestEnsureServiceEnv_WarnsLoudlyWhenEnvRemoved(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "")
	t.Setenv("RUNWISP_AUTH", "")
	inst := &pwInstaller{
		envPath:   "/etc/systemd/system/runwisp.service.d/runwisp-env.conf",
		envChange: autostart.DropInRemoved,
	}
	var out bytes.Buffer
	ensureServiceEnv(newInstallTestCmd(&out, &bytes.Buffer{}), inst, autostart.InstallOptions{})

	assert.Contains(t, out.String(), "WARNING")
	assert.Contains(t, out.String(), inst.envPath)
	assert.NotContains(t, out.String(), "Saved your RUNWISP_* environment",
		"a removed drop-in must never be reported as saved")
	assert.Equal(t, 1, inst.restarts, "the daemon must restart to pick up the reverted environment")
}

func TestEnsureServiceEnv_GeneratesPasswordPrintsAndRestarts(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "")
	t.Setenv("RUNWISP_AUTH", "")
	inst := &pwInstaller{path: "/etc/systemd/system/runwisp.service.d/password.conf", wrote: true}
	var out bytes.Buffer
	ensureServiceEnv(newInstallTestCmd(&out, &bytes.Buffer{}), inst, autostart.InstallOptions{})

	require.True(t, inst.ensureCalled)
	assert.NotEmpty(t, inst.ensurePassword, "a fresh password must be generated")
	assert.Equal(t, 1, inst.restarts, "must restart so the daemon picks up the drop-in")
	assert.Contains(t, out.String(), inst.ensurePassword, "the generated password is printed once")
	assert.Contains(t, out.String(), inst.path)
}

func TestEnsureServiceEnv_SkipsRestartWhenNothingChanged(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "")
	t.Setenv("RUNWISP_AUTH", "")
	// wrote=false with a non-empty path means an existing password drop-in was
	// left alone; envChange=DropInUnchanged (default) means the env drop-in already
	// matched — neither needs a restart.
	inst := &pwInstaller{path: "/etc/systemd/system/runwisp.service.d/password.conf", wrote: false}
	var out bytes.Buffer
	ensureServiceEnv(newInstallTestCmd(&out, &bytes.Buffer{}), inst, autostart.InstallOptions{})

	assert.Zero(t, inst.restarts, "nothing changed — no restart needed")
	assert.NotContains(t, out.String(), manualPasswordHint, "a working drop-in needs no manual hint")
}
