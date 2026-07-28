// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func systemInstallOpts(binary string) InstallOptions {
	opts := defaultInstallOpts(binary)
	opts.System = true
	opts.Config = "/etc/runwisp/runwisp.toml"
	opts.DataDir = "/var/lib/runwisp"
	return opts
}

func TestDiscoverCronUnit_FirstCandidateFound(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)

	unit, err := inst.discoverCronUnit(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cron.service", unit)
	assert.Zero(t, cmd.Remaining())
}

func TestDiscoverCronUnit_FallsThroughToCrond(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("not-found\n\n\n"), nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "crond.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)

	unit, err := inst.discoverCronUnit(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "crond.service", unit)
}

func TestDiscoverCronUnit_NoneFound(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	for _, name := range cronUnitCandidates {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", name},
			[]byte("not-found\n\n\n"), nil, nil)
	}

	_, err := inst.discoverCronUnit(context.Background())
	assert.ErrorIs(t, err, ErrNoCronUnit)
}

// TestComputePlan_DiscoveryOnlyRunsWhenTakeOverCronSet is the regression the
// plan calls out explicitly: a plain ComputePlan (no --take-over-cron) must
// never touch the Runner to look for a cron unit, or every other
// ComputePlan test would need scripting it doesn't have.
func TestComputePlan_DiscoveryOnlyRunsWhenTakeOverCronSet(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	cmd.Expect("loginctl", []string{"show-user", "--property=Linger", "alice"},
		[]byte("Linger=no\n"), nil, nil)

	opts := defaultInstallOpts(binary)
	opts.TakeOverCron = false
	plan, err := inst.ComputePlan(context.Background(), opts)
	require.NoError(t, err)
	assert.Empty(t, plan.CronUnit)
	assert.Zero(t, cmd.Remaining())
}

func TestComputePlan_TakeOverCronDiscoversAndRecordsMarker(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	inst.deps.Euid = 0

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)

	plan, err := inst.ComputePlan(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "cron.service", plan.CronUnit)
	assert.Contains(t, plan.UnitContent, "# runwisp-masked-cron: cron.service")
	actions := stepActions(plan.Steps)
	assert.Contains(t, actions, ActionStopCron)
	assert.Contains(t, actions, ActionMaskCron)
}

// TestComputePlan_CarriesForwardExistingMarker proves the fix for the
// oscillation bug: a plain re-install (TakeOverCron false) against a unit
// that already recorded a take-over must keep recording it, or the marker
// disappears and cron stays masked with nothing on disk saying who did it.
func TestComputePlan_CarriesForwardExistingMarker(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	opts := systemInstallOpts(binary)
	inst.deps.Euid = 0

	takenOver := opts
	takenOver.maskedCronUnit = "cron.service"
	body, _, err := inst.renderUnit(takenOver)
	require.NoError(t, err)
	unitPath := inst.unitPath(true)
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	// No Linger probe for a system-wide unit; no systemctl show for cron
	// either, since TakeOverCron is false this time — only an FS read.
	plan, err := inst.ComputePlan(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "cron.service", plan.CronUnit)
	assert.Equal(t, PlanNoop, plan.Kind, "carried-forward marker must not itself cause drift")
	assert.Zero(t, cmd.Remaining())
}

func TestApplyInstall_TakeOverCron_StopMaskThenStart(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	prompter.YesNo = []bool{true}
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	// stopAndMaskCron re-probes ActiveState right before acting.
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.NoError(t, err)
	assert.Zero(t, cmd.Remaining())
	assert.Contains(t, out.String(), "Stopping cron.service")
	assert.Contains(t, out.String(), "Masking cron.service")

	written, err := fs.ReadFile(inst.unitPath(true))
	require.NoError(t, err)
	assert.Contains(t, string(written), "# runwisp-masked-cron: cron.service")
}

func TestApplyInstall_TakeOverCron_RollsBackOnStartFailure(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	prompter.YesNo = []bool{true}
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"},
		nil, []byte("bind: address already in use"), assertErrFake("enable failed"))
	// Rollback: cron was active before, so unmask AND start it again.
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "rollback must run every scripted call")
	assert.Contains(t, out.String(), "Unmasking cron.service")
	assert.Contains(t, out.String(), "Starting cron.service")
}

func TestApplyInstall_TakeOverCron_NoRestartWhenCronWasAlreadyStopped(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	prompter.YesNo = []bool{true}
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"},
		nil, []byte("failed"), assertErrFake("enable failed"))
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "no start call scripted — must not be attempted for already-inactive cron")
	assert.NotContains(t, out.String(), "Starting cron.service")
}

func TestComputeUninstallPlan_UnmasksOnlyOwnMarker(t *testing.T) {
	inst, fs, _, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.maskedCronUnit = "cron.service"
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := inst.unitPath(true)
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{System: true})
	require.NoError(t, err)
	assert.Equal(t, "cron.service", plan.CronUnit)
	assert.Contains(t, stepActions(plan.Steps), ActionUnmaskCron)
}

func TestComputeUninstallPlan_NoMarkerMeansNoUnmaskStep(t *testing.T) {
	inst, fs, _, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := inst.unitPath(true)
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{System: true})
	require.NoError(t, err)
	assert.Empty(t, plan.CronUnit)
	assert.NotContains(t, stepActions(plan.Steps), ActionUnmaskCron)
}

func TestApplyUninstall_UnmasksAndRestartsCron(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.maskedCronUnit = "cron.service"
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	unitPath := inst.unitPath(true)
	require.NoError(t, fs.WriteFile(unitPath, body, 0644))

	cmd.Expect("systemctl", []string{"stop", "runwisp.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"disable", "runwisp.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)

	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{System: true})
	require.NoError(t, err)

	out := &bytes.Buffer{}
	err = inst.applyUninstall(context.Background(), plan, UninstallOptions{System: true}, out)
	require.NoError(t, err)
	assert.Zero(t, cmd.Remaining())
	assert.Contains(t, out.String(), "Unmasking cron.service")
	assert.Contains(t, out.String(), "Starting cron.service")
}

func TestStatus_CronRow_MaskedByUs(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.maskedCronUnit = "cron.service"
	body, _, err := inst.renderUnit(opts)
	require.NoError(t, err)
	require.NoError(t, fs.WriteFile(inst.unitPath(true), body, 0644))

	cmd.Expect("systemctl", []string{"is-enabled", "runwisp.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"is-active", "runwisp.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"show", "-p", "ActiveEnterTimestamp", "--value", "runwisp.service"}, []byte("n/a\n"), nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nmasked\n"), nil, nil)

	st, err := inst.Status(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, "cron.service", st.CronUnit)
	assert.True(t, st.CronMasked)
	assert.False(t, st.CronActive)
}

func TestStatus_CronRow_OmittedWhenNoMarker(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)

	cmd.Expect("systemctl", []string{"is-enabled", "runwisp.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"is-active", "runwisp.service"}, nil, nil, assertErrFake("no unit"))
	cmd.Expect("systemctl", []string{"show", "-p", "ActiveEnterTimestamp", "--value", "runwisp.service"}, []byte("n/a\n"), nil, nil)

	st, err := inst.Status(context.Background(), opts)
	require.NoError(t, err)
	assert.Empty(t, st.CronUnit)
	assert.Zero(t, cmd.Remaining(), "no cron probe without a marker")
}

func TestRender_TakeOverCron_IncludesMarker(t *testing.T) {
	inst, _, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)

	body, err := inst.Render(opts)
	require.NoError(t, err)
	assert.Contains(t, string(body), "# runwisp-masked-cron: cron.service")
}
