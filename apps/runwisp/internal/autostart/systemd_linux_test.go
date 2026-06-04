// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFakeInstaller wires a systemdInstaller against FakeFS + FakeRunner
// and a scripted prompter. The returned binary path points at a real
// temp file (because the installer hashes its content).
func newFakeInstaller(t *testing.T, wsl bool) (*systemdInstaller, *FakeFS, *FakeRunner, *ScriptedPrompter, string) {
	t.Helper()
	binaryPath := filepath.Join(t.TempDir(), "runwisp")
	require.NoError(t, os.WriteFile(binaryPath, []byte("fake-binary-content"), 0755))
	fs := NewFakeFS()
	cmd := NewFakeRunner()
	prompter := &ScriptedPrompter{}
	deps := Deps{
		FS:          fs,
		Cmd:         cmd,
		Prompter:    prompter,
		Stdout:      &bytes.Buffer{},
		Stderr:      &bytes.Buffer{},
		Home:        "/home/alice",
		User:        "alice",
		WSL:         wsl,
		Fingerprint: "bright-falcon",
		AutoOK:      true,
		StdinIsTTY:  false,
	}
	return &systemdInstaller{deps: deps}, fs, cmd, prompter, binaryPath
}

func defaultInstallOpts(binary string) InstallOptions {
	return InstallOptions{
		Binary:  binary,
		Config:  "/home/alice/.config/runwisp/runwisp.toml",
		DataDir: "/home/alice/.local/share/runwisp",
		Host:    "127.0.0.1",
		Port:    9477,
	}
}

func TestSystemdComputePlan_FreshInstall(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=no\n"), nil, nil)

	plan, err := inst.ComputePlan(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	assert.Equal(t, PlanInstall, plan.Kind)
	assert.Equal(t, "/home/alice/.config/systemd/user/runwisp-bright-falcon.service", plan.UnitPath)
	assert.False(t, plan.LingerOn)

	// Step sequence: WriteUnit, daemon-reload, EnableLinger, EnableService.
	actions := stepActions(plan.Steps)
	assert.Equal(t,
		[]Action{ActionWriteUnit, ActionDaemonReload, ActionEnableLinger, ActionEnableService},
		actions,
	)
}

func TestSystemdComputePlan_LingerAlreadyOnSkipsStep(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=yes\n"), nil, nil)

	plan, err := inst.ComputePlan(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	assert.True(t, plan.LingerOn)
	actions := stepActions(plan.Steps)
	for _, a := range actions {
		assert.NotEqual(t, ActionEnableLinger, a, "linger step must be skipped when Linger=yes")
	}
}

func TestSystemdComputePlan_WSLAppendsPostscript(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, true)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=no\n"), nil, nil)

	plan, err := inst.ComputePlan(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	actions := stepActions(plan.Steps)
	assert.Contains(t, actions, ActionPrintWSLPostscript)
}

func TestSystemdComputePlan_NoopWhenInstalled(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=yes\n"), nil, nil)

	// First-time install to produce the desired body, then write it
	// to FakeFS so the second ComputePlan call sees a match.
	body, _, err := inst.renderUnit(defaultInstallOpts(binary))
	require.NoError(t, err)
	require.NoError(t, fs.WriteFile("/home/alice/.config/systemd/user/runwisp-bright-falcon.service", body, 0644))

	plan, err := inst.ComputePlan(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	assert.Equal(t, PlanNoop, plan.Kind)
	assert.Empty(t, plan.Steps)
}

func TestSystemdComputePlan_ConflictRequiresForce(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=yes\n"), nil, nil)

	require.NoError(t, fs.WriteFile(
		"/home/alice/.config/systemd/user/runwisp-bright-falcon.service",
		[]byte("# hand-written by alice\n[Unit]\nDescription=mine\n"),
		0644,
	))

	plan, err := inst.ComputePlan(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	assert.Equal(t, PlanConflict, plan.Kind)
}

func TestSystemdInstall_HappyPath(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)
	prompter.YesNo = []bool{true} // accept the Proceed? prompt

	// runRenderUnit calls renderUnit via ComputePlan which calls
	// checkLinger; the Install path then calls daemon-reload,
	// loginctl enable-linger, enable --now.
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=no\n"), nil, nil)
	cmd.Expect("systemctl", []string{"--user", "daemon-reload"}, nil, nil, nil)
	cmd.Expect("sudo", []string{"loginctl", "enable-linger", "alice"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"--user", "enable", "--now", "runwisp-bright-falcon.service"}, nil, nil, nil)

	// `preflight` checks config exists.
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.NoError(t, err)

	written, err := fs.ReadFile("/home/alice/.config/systemd/user/runwisp-bright-falcon.service")
	require.NoError(t, err)
	assert.Contains(t, string(written), ManagedMarker)
	assert.Contains(t, string(written), opts.Binary)
	assert.Equal(t, 0, cmd.Remaining(), "every scripted command must have been consumed")
}

func TestSystemdInstall_ConfigMissingErrors(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=yes\n"), nil, nil)

	err := inst.Install(context.Background(), defaultInstallOpts(binary), &bytes.Buffer{})
	assert.ErrorIs(t, err, ErrConfigMissing)
}

func TestSystemdUninstall_Symmetric(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)
	prompter.YesNo = []bool{true}
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	cmd.Expect("systemctl", []string{"--user", "stop", "runwisp-bright-falcon.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"--user", "disable", "runwisp-bright-falcon.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"--user", "daemon-reload"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err = inst.Uninstall(context.Background(), UninstallOptions{DataDir: opts.DataDir}, out)
	require.NoError(t, err)

	_, err = fs.ReadFile(unitPath)
	assert.Error(t, err, "unit file must be removed")
	assert.Contains(t, out.String(), "Uninstalled.")
}

func TestSystemdUninstall_PurgeRequiresLiteralWord(t *testing.T) {
	inst, fs, _, prompter, binary := newFakeInstaller(t, false)
	body, _, err := inst.renderUnit(defaultInstallOpts(binary))
	require.NoError(t, err)
	unitPath := "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	// Operator confirmed "Proceed?", then typed something other than
	// "delete" — uninstall must abort before touching the data dir.
	prompter.YesNo = []bool{true}
	prompter.Literals = []string{"yes"}

	err = inst.Uninstall(context.Background(),
		UninstallOptions{Purge: true, DataDir: "/home/alice/runwisp"},
		&bytes.Buffer{},
	)
	assert.ErrorIs(t, err, ErrAborted)
}

func TestSystemdRender_Print(t *testing.T) {
	inst, _, _, _, binary := newFakeInstaller(t, false)
	body, err := inst.Render(defaultInstallOpts(binary))
	require.NoError(t, err)
	s := string(body)
	assert.True(t, strings.HasPrefix(s, ManagedMarker),
		"render output must start with the managed marker so --print writes a ready-to-cp file")
	assert.Contains(t, s, "ExecStart=")
	assert.Contains(t, s, "Type=simple")
	assert.Contains(t, s, "WantedBy=default.target")
}

func TestSystemdStatus_NotInstalled(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)

	// systemctl is-enabled / is-active / show / loginctl all return
	// errors when nothing is installed; FakeRunner needs explicit
	// entries even for negative cases.
	cmd.Expect("systemctl", []string{"--user", "is-enabled", "runwisp-bright-falcon.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"--user", "is-active", "runwisp-bright-falcon.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"--user", "show", "-p", "ActiveEnterTimestamp", "--value", "runwisp-bright-falcon.service"}, []byte("n/a\n"), nil, nil)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"}, []byte("Linger=no\n"), nil, nil)

	st, err := inst.Status(context.Background(), defaultInstallOpts(binary))
	require.NoError(t, err)
	assert.False(t, st.UnitExists)
	assert.False(t, st.Installed)
}

func stepActions(steps []Step) []Action {
	out := make([]Action, len(steps))
	for i, s := range steps {
		out[i] = s.Action
	}
	return out
}

// assertErrFake returns a sentinel error tagged with a message so the
// stub-vs-error path is clear in test failures.
func assertErrFake(msg string) error {
	return fakeErr(msg)
}

type fakeErr string

func (f fakeErr) Error() string { return string(f) }

func TestSystemdStopRestart_RunSystemctlUserVerbs(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)

	cmd.Expect("systemctl", []string{"--user", "stop", "runwisp-bright-falcon.service"}, nil, nil, nil)
	require.NoError(t, inst.Stop(context.Background(), opts))

	cmd.Expect("systemctl", []string{"--user", "restart", "runwisp-bright-falcon.service"}, nil, nil, nil)
	require.NoError(t, inst.Restart(context.Background(), opts))

	assert.Zero(t, cmd.Remaining())
}

func TestSystemdStopRestart_SystemWideUsesSudo(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)
	opts.System = true

	cmd.Expect("sudo", []string{"systemctl", "stop", "runwisp-bright-falcon.service"}, nil, nil, nil)
	require.NoError(t, inst.Stop(context.Background(), opts))

	cmd.Expect("sudo", []string{"systemctl", "restart", "runwisp-bright-falcon.service"}, nil, nil, nil)
	require.NoError(t, inst.Restart(context.Background(), opts))

	assert.Zero(t, cmd.Remaining())
}

func TestSystemdStop_SurfacesStderr(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("systemctl", []string{"--user", "stop", "runwisp-bright-falcon.service"},
		nil, []byte("Failed to stop unit"), errors.New("exit status 1"))

	err := inst.Stop(context.Background(), defaultInstallOpts(binary))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to stop unit")
}
