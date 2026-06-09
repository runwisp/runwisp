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
	"time"

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

func TestSystemdNew_HappyPath(t *testing.T) {
	inst, err := New(Deps{
		Home:        "/home/alice",
		User:        "alice",
		Fingerprint: "bright-falcon",
	})
	require.NoError(t, err)
	require.NotNil(t, inst)
}

func TestSystemdNew_Validation(t *testing.T) {
	cases := []struct {
		name string
		deps Deps
		want string
	}{
		{"missing-home", Deps{User: "alice", Fingerprint: "fp"}, "HOME is not set"},
		{"missing-user", Deps{Home: "/h", Fingerprint: "fp"}, "user is not set"},
		{"missing-fingerprint", Deps{Home: "/h", User: "alice"}, "fingerprint is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.deps)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestWSLTaskSchedulerPostscript_KnownDistro(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu-24.04")
	got := wslTaskSchedulerPostscript()
	assert.Contains(t, got, "Ubuntu-24.04")
	assert.Contains(t, got, "RunWispBoot")
	assert.Contains(t, got, "PowerShell")
}

func TestWSLTaskSchedulerPostscript_FallbackPlaceholder(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	got := wslTaskSchedulerPostscript()
	assert.Contains(t, got, "<distro>")
}

func TestParseSystemdTimestamp(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantZero bool
	}{
		{"empty", "", true},
		{"na", "n/a", true},
		{"zero-prefix", "0", true},
		{"weekday-layout", "Mon 2024-09-30 12:34:56 UTC", false},
		{"plain-layout", "2024-09-30 12:34:56 UTC", false},
		{"unparseable", "yesterday", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSystemdTimestamp(tc.in)
			if tc.wantZero {
				assert.True(t, got.IsZero(), "want zero time, got %v", got)
			} else {
				assert.False(t, got.IsZero(), "want non-zero time")
				assert.Equal(t, 2024, got.Year())
				assert.Equal(t, time.September, got.Month())
				assert.Equal(t, 30, got.Day())
			}
		})
	}
}

func TestSystemdRunDaemonReload_SystemWideSuccess(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("sudo", []string{"systemctl", "daemon-reload"}, nil, nil, nil)
	require.NoError(t, inst.runDaemonReload(context.Background(), true))
}

func TestSystemdRunDaemonReload_SystemWideError(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("sudo", []string{"systemctl", "daemon-reload"}, nil, []byte("permission denied"),
		assertErrFake("sudo failed"))
	err := inst.runDaemonReload(context.Background(), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon-reload")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestSystemdRunDaemonReload_UserModeError(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("systemctl", []string{"--user", "daemon-reload"}, nil, []byte("nope"),
		assertErrFake("user failed"))
	err := inst.runDaemonReload(context.Background(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--user daemon-reload")
}

func TestSystemdRunEnableNow_SystemWideSuccess(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("sudo", []string{"systemctl", "enable", "--now", "runwisp-bright-falcon.service"}, nil, nil, nil)
	require.NoError(t, inst.runEnableNow(context.Background(), true))
}

func TestSystemdRunEnableNow_SystemWideError(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("sudo", []string{"systemctl", "enable", "--now", "runwisp-bright-falcon.service"},
		nil, []byte("bad unit"), assertErrFake("sudo failed"))
	err := inst.runEnableNow(context.Background(), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "enable --now")
}

func TestSystemdRunEnableNow_UserModeError(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	cmd.Expect("systemctl", []string{"--user", "enable", "--now", "runwisp-bright-falcon.service"},
		nil, []byte("denied"), assertErrFake("user failed"))
	err := inst.runEnableNow(context.Background(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--user enable --now")
}

func TestSystemdApplyUninstall_StopAndDisableErrorsAreWarnings(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"
	require.NoError(t, fs.WriteFile(unitPath, body, 0o644))

	// Both stop and disable fail; the function should continue, still
	// remove the unit, and warn on daemon-reload errors too.
	cmd.Expect("systemctl", []string{"--user", "stop", "runwisp-bright-falcon.service"},
		nil, []byte("inactive"), assertErrFake("stop failed"))
	cmd.Expect("systemctl", []string{"--user", "disable", "runwisp-bright-falcon.service"},
		nil, []byte("disabled"), assertErrFake("disable failed"))
	cmd.Expect("systemctl", []string{"--user", "daemon-reload"},
		nil, []byte("reload err"), assertErrFake("reload failed"))

	out := &bytes.Buffer{}
	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{})
	require.NoError(t, err)
	err = inst.applyUninstall(context.Background(), plan, UninstallOptions{DataDir: opts.DataDir}, out)
	require.NoError(t, err)

	s := out.String()
	assert.Contains(t, s, "Warning: systemctl --user stop")
	assert.Contains(t, s, "Warning: systemctl --user disable")
	assert.Contains(t, s, "Warning: systemctl --user daemon-reload")
	assert.Contains(t, s, "Removed "+unitPath)
}

func TestSystemdApplyUninstall_PurgeRemovesDataDir(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"
	require.NoError(t, fs.WriteFile(unitPath, body, 0o644))

	// Real temp data dir so os.RemoveAll has something to act on.
	dataDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "marker"), []byte("x"), 0o600))

	cmd.Expect("systemctl", []string{"--user", "stop", "runwisp-bright-falcon.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"--user", "disable", "runwisp-bright-falcon.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"--user", "daemon-reload"}, nil, nil, nil)

	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{})
	require.NoError(t, err)

	out := &bytes.Buffer{}
	err = inst.applyUninstall(context.Background(), plan, UninstallOptions{Purge: true, DataDir: dataDir}, out)
	require.NoError(t, err)

	_, err = os.Stat(dataDir)
	assert.True(t, os.IsNotExist(err), "data dir must be removed")
	assert.Contains(t, out.String(), "Purged data dir "+dataDir)
}

func TestSystemdStatus_Installed_FullPath(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	opts := defaultInstallOpts(binary)

	// Write a managed unit (using a real render so markers match).
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := "/home/alice/.config/systemd/user/runwisp-bright-falcon.service"
	require.NoError(t, fs.WriteFile(unitPath, body, 0o644))

	// Data dir on real disk so isDirWritable works, plus registered in
	// FakeFS so Status's Stat returns IsDir==true.
	dataDir := t.TempDir()
	opts.DataDir = dataDir
	require.NoError(t, fs.MkdirAll(dataDir, 0o755))

	cmd.Expect("systemctl", []string{"--user", "is-enabled", "runwisp-bright-falcon.service"},
		[]byte("enabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"--user", "is-active", "runwisp-bright-falcon.service"},
		[]byte("active\n"), nil, nil)
	cmd.Expect("systemctl", []string{"--user", "show", "-p", "ActiveEnterTimestamp", "--value", "runwisp-bright-falcon.service"},
		[]byte("Mon 2024-09-30 12:34:56 UTC\n"), nil, nil)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=yes\n"), nil, nil)

	st, err := inst.Status(context.Background(), opts)
	require.NoError(t, err)
	assert.True(t, st.UnitExists)
	assert.True(t, st.UnitManaged)
	assert.True(t, st.Installed)
	assert.True(t, st.BinaryExists)
	assert.True(t, st.Autostart)
	assert.True(t, st.Running)
	assert.True(t, st.Linger)
	assert.True(t, st.DataDirWritable)
	assert.False(t, st.LastStart.IsZero())
	assert.NotEmpty(t, st.ExpectedConfigHash)
	assert.NotEmpty(t, st.BinaryOnDiskSHA)
	assert.Contains(t, st.LogsHint, "journalctl")
}

func TestSystemdEnvPath_FallsBackToDefaultWhenUnset(t *testing.T) {
	t.Setenv("PATH", "")
	assert.Equal(t, defaultPATH, envPath())
}

func TestSystemdEnvPath_UsesEnvWhenSet(t *testing.T) {
	t.Setenv("PATH", "/custom/bin")
	assert.Equal(t, "/custom/bin", envPath())
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
