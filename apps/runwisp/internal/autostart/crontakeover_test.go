// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/importer"
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

	unit, _, _, err := inst.discoverCronUnit(context.Background())
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

	unit, _, _, err := inst.discoverCronUnit(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "crond.service", unit)
}

// CronStatus is what the install prompt consults before asking whether to
// retire cron, so "no cron on this host" has to be a plain answer rather
// than the error discoverCronUnit returns to a caller who asked for a
// take-over outright.
func TestCronStatus_NoCronUnitIsNotAnError(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	for _, name := range importer.CronUnits() {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", name},
			[]byte("not-found\n\n\n"), nil, nil)
	}

	unit, active, err := inst.CronStatus(context.Background())
	require.NoError(t, err)
	assert.Empty(t, unit)
	assert.False(t, active)
}

func TestCronStatus_ReportsRunningUnit(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	// One probe answers both discovery and the active-state read.
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)

	unit, active, err := inst.CronStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cron.service", unit)
	assert.True(t, active)
	assert.Zero(t, cmd.Remaining())
}

func TestCronStatus_ReportsStoppedUnit(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nenabled\n"), nil, nil)

	unit, active, err := inst.CronStatus(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "cron.service", unit)
	assert.False(t, active, "a stopped cron is nothing to take over")
}

func TestDiscoverCronUnit_NoneFound(t *testing.T) {
	inst, _, cmd, _, _ := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	for _, name := range importer.CronUnits() {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", name},
			[]byte("not-found\n\n\n"), nil, nil)
	}

	_, _, _, err := inst.discoverCronUnit(context.Background())
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

// TestApplyInstall_TakeOverCron_NoRollbackOnStopFailure is the B2 correction:
// a failed `systemctl stop` means cron was never actually stopped or masked,
// so there is nothing for the rollback to undo. Unmasking (or starting) a
// unit that was never touched would just poke cron for no reason.
func TestApplyInstall_TakeOverCron_NoRollbackOnStopFailure(t *testing.T) {
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
	cmd.Expect("systemctl", []string{"stop", "cron.service"},
		nil, []byte("Failed to stop unit: access denied"), assertErrFake("stop failed"))

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "no mask, unmask, or start scripted — none must be attempted")
	assert.NotContains(t, out.String(), "Masking")
	assert.NotContains(t, out.String(), "Unmasking")
	assert.NotContains(t, out.String(), "Starting cron.service")
}

// TestApplyInstall_TakeOverCron_NoRollbackOnProbeFailure is the other half:
// the re-probe inside stopAndMaskCron fails before `stop` is ever attempted,
// so cron was never touched at all.
func TestApplyInstall_TakeOverCron_NoRollbackOnProbeFailure(t *testing.T) {
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
		nil, []byte("connection refused"), assertErrFake("probe failed"))

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "no stop, mask, unmask, or start scripted — none must be attempted")
	assert.NotContains(t, out.String(), "Stopping")
	assert.NotContains(t, out.String(), "Unmasking")
}

// TestApplyInstall_TakeOverCron_RollsBackOnMaskFailure is the regression for the
// bug where a successful `stop` followed by a failed `mask` left the box with
// cron stopped, unmasked, and never restarted — no scheduler running at all —
// because the error from stopAndMaskCron short-circuited the install before any
// rollback ran. It must restore cron the same way a failed `enable --now`
// already does.
func TestApplyInstall_TakeOverCron_RollsBackOnMaskFailure(t *testing.T) {
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
	cmd.Expect("systemctl", []string{"mask", "cron.service"},
		nil, []byte("Failed to mask unit: access denied"), assertErrFake("mask failed"))
	// Rollback: cron was active before, so unmask AND start it again — `enable
	// --now` for RunWisp is never reached.
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "rollback must run every scripted call")
	assert.Contains(t, out.String(), "Unmasking cron.service")
	assert.Contains(t, out.String(), "Starting cron.service")
}

// TestApplyInstall_TakeOverCron_MaskFailureNoRestartWhenCronWasAlreadyStopped
// is the other half: the rollback restores the prior scheduler state, which
// means it must NOT start cron back up when cron was already inactive before
// the take-over began.
func TestApplyInstall_TakeOverCron_MaskFailureNoRestartWhenCronWasAlreadyStopped(t *testing.T) {
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
	cmd.Expect("systemctl", []string{"mask", "cron.service"},
		nil, []byte("Failed to mask unit: access denied"), assertErrFake("mask failed"))
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	err := inst.Install(context.Background(), opts, out)
	require.Error(t, err)
	assert.Zero(t, cmd.Remaining(), "no start call scripted — must not be attempted for already-inactive cron")
	assert.NotContains(t, out.String(), "Starting cron.service")
}

// TestInstall_NoopReassertRollsBackOnMaskFailure covers the other caller of the
// same stopAndMaskCron helper: the reassert/no-op repair path taken when cron
// has come back since a completed take-over. It must roll back on a mask
// failure exactly like a fresh install does.
func TestInstall_NoopReassertRollsBackOnMaskFailure(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	for range 3 {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
			[]byte("loaded\nactive\nenabled\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"},
		nil, []byte("Failed to mask unit: access denied"), assertErrFake("mask failed"))
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	require.Error(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "rollback must run every scripted call")
	assert.Contains(t, out.String(), "Unmasking cron.service")
	assert.Contains(t, out.String(), "Starting cron.service")
}

// TestInstall_NoopReassertNoRollbackOnStopFailure is the reassert-path half of
// the same B2 correction: a failed `systemctl stop` during the repair means
// cron was never actually stopped or masked, so there is nothing to roll back.
func TestInstall_NoopReassertNoRollbackOnStopFailure(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	for range 3 {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
			[]byte("loaded\nactive\nenabled\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"stop", "cron.service"},
		nil, []byte("Failed to stop unit: access denied"), assertErrFake("stop failed"))

	out := &bytes.Buffer{}
	require.Error(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "no mask, unmask, or start scripted — none must be attempted")
	assert.NotContains(t, out.String(), "Unmasking")
	assert.NotContains(t, out.String(), "Starting cron.service")
}

// TestInstall_NoopReassertNoRollbackOnProbeFailure covers the probe-failure
// half: stopAndMaskCron's own re-probe fails before `stop` is ever attempted.
func TestInstall_NoopReassertNoRollbackOnProbeFailure(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	for range 2 {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
			[]byte("loaded\nactive\nenabled\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		nil, []byte("connection refused"), assertErrFake("probe failed"))

	out := &bytes.Buffer{}
	require.Error(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "no stop, mask, unmask, or start scripted — none must be attempted")
	assert.NotContains(t, out.String(), "Stopping")
	assert.NotContains(t, out.String(), "Unmasking")
}

// takenOverUnitOnDisk writes the unit a completed take-over leaves behind, so
// ComputePlan sees no drift and returns PlanNoop — the state `runwisp takeover`
// finds on a box it has already been run on.
func takenOverUnitOnDisk(t *testing.T, inst *systemdInstaller, fs *FakeFS, opts InstallOptions) {
	t.Helper()
	recorded := opts
	recorded.maskedCronUnit = "cron.service"
	body, _, err := inst.renderUnit(recorded)
	require.NoError(t, err)
	require.NoError(t, fs.WriteFile(inst.unitPath(true), body, 0644))
}

// Cron coming back after a take-over (an operator unmasking it, a package
// upgrade) used to leave `runwisp takeover` unable to fix it: the unit already
// matched, so the install reported "Already installed ✓" and returned without
// looking at cron — two schedulers, and a success message.
func TestInstall_NoopReassertsTakeOverWhenCronCameBack(t *testing.T) {
	// No answer is queued on the prompter, and a ScriptedPrompter errors when
	// consulted without one — so "the repair asks no second question" is
	// enforced here, not merely assumed. Consent for the mask is the caller's,
	// taken once against a plan that spells it out.
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	// Discovery for the plan, then the standing-state probe, then the
	// stop/mask/start repair.
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\nactive\nenabled\n"), nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	require.NoError(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining())
	assert.Contains(t, out.String(), "Already installed")
	assert.Contains(t, out.String(), "cron.service is back since the take-over")
	assert.Contains(t, out.String(), "Masking cron.service")
}

// The repair keeps the install's own ordering: if RunWisp will not start, cron
// goes back rather than leaving the box with no scheduler.
func TestInstall_NoopRepairRollsBackWhenServiceWontStart(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	for range 3 {
		cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
			[]byte("loaded\nactive\nenabled\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"},
		nil, []byte("bind: address already in use"), assertErrFake("enable failed"))
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)

	out := &bytes.Buffer{}
	require.Error(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "rollback must run every scripted call")
	assert.Contains(t, out.String(), "Unmasking cron.service")
}

// Nothing to repair costs one probe and says nothing: a re-run of `takeover` on
// a box where the take-over still holds is not an excuse to bounce cron.
func TestInstall_NoopLeavesAlreadyRetiredCronAlone(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	takenOverUnitOnDisk(t, inst, fs, opts)

	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nmasked\n"), nil, nil)
	cmd.Expect("systemctl", []string{"show", "-p", "LoadState,ActiveState,UnitFileState", "--value", "cron.service"},
		[]byte("loaded\ninactive\nmasked\n"), nil, nil)

	out := &bytes.Buffer{}
	require.NoError(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "no stop, no mask, no enable scripted — none must be attempted")
	assert.NotContains(t, out.String(), "Masking")
}

// A plain re-install carries the marker forward without being asked to retire
// anything, so it must not touch cron either way.
func TestInstall_NoopWithoutTakeOverFlagNeverProbesCron(t *testing.T) {
	inst, fs, cmd, _, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	takenOverUnitOnDisk(t, inst, fs, opts)

	out := &bytes.Buffer{}
	require.NoError(t, inst.Install(context.Background(), opts, out))
	assert.Zero(t, cmd.Remaining(), "no systemctl call scripted for a plain no-op re-install")
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

const cronShowState = "show -p LoadState,ActiveState,UnitFileState --value cron.service"

// installTakeover runs a take-over install that succeeds, with cron probed as
// "<ActiveState>\n<UnitFileState>" before RunWisp touched it.
func installTakeover(t *testing.T, inst *systemdInstaller, fs *FakeFS, cmd *FakeRunner, prompter *ScriptedPrompter, opts InstallOptions, cronState string) {
	t.Helper()
	opts.TakeOverCron = true
	prompter.YesNo = []bool{true}
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))
	for range 2 { // discovery, then stopAndMaskCron's re-probe
		cmd.Expect("systemctl", strings.Fields(cronShowState), []byte("loaded\n"+cronState+"\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"}, nil, nil, nil)
	require.NoError(t, inst.Install(context.Background(), opts, &bytes.Buffer{}))
}

// callsSince renders every systemctl call made after the first n as
// "verb args…", for asserting what an undo did or did not touch.
func callsSince(cmd *FakeRunner, n int) []string {
	var out []string
	for _, c := range cmd.Log()[n:] {
		out = append(out, strings.Join(c.Args, " "))
	}
	return out
}

func uninstallSystem(t *testing.T, inst *systemdInstaller, cmd *FakeRunner) []string {
	t.Helper()
	plan, err := inst.ComputeUninstallPlan(context.Background(), UninstallOptions{System: true})
	require.NoError(t, err)
	// Scripted so they succeed if called; the caller asserts whether they were.
	cmd.Expect("systemctl", []string{"unmask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"start", "cron.service"}, nil, nil, nil)
	n := len(cmd.Log())
	require.NoError(t, inst.applyUninstall(context.Background(), plan, UninstallOptions{System: true}, &bytes.Buffer{}))
	return callsSince(cmd, n)
}

// Finding RW-37#13: an operator who had masked cron themselves before the
// take-over must not get it back on uninstall; RunWisp never owned that mask.
func TestTakeover_UninstallLeavesOperatorMaskedCronMasked(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "inactive\nmasked")

	calls := uninstallSystem(t, inst, cmd)
	assert.NotContains(t, calls, "unmask cron.service")
	assert.NotContains(t, calls, "start cron.service")
}

// Cron that was enabled but stopped at take-over time comes back unmasked
// (so it starts on the next boot, as before) but is not started now.
func TestTakeover_UninstallRestoresStoppedCronStopped(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "inactive\nenabled")

	calls := uninstallSystem(t, inst, cmd)
	assert.Contains(t, calls, "unmask cron.service")
	assert.NotContains(t, calls, "start cron.service")
}

func TestTakeover_UninstallRestartsCronThatWasRunning(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "active\nenabled")

	calls := uninstallSystem(t, inst, cmd)
	assert.Contains(t, calls, "unmask cron.service")
	assert.Contains(t, calls, "start cron.service")
}

// The rollback half of RW-37#13: a failed `enable --now` must not unmask a
// cron the operator had masked before RunWisp touched it.
func TestTakeover_RollbackLeavesOperatorMaskedCronMasked(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	opts.TakeOverCron = true
	prompter.YesNo = []bool{true}
	require.NoError(t, fs.WriteFile(opts.Config, []byte("[scheduler]\n"), 0644))
	for range 2 {
		cmd.Expect("systemctl", strings.Fields(cronShowState), []byte("loaded\ninactive\nmasked\n"), nil, nil)
	}
	cmd.Expect("systemctl", []string{"daemon-reload"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"stop", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"mask", "cron.service"}, nil, nil, nil)
	cmd.Expect("systemctl", []string{"enable", "--now", "runwisp.service"}, nil, []byte("failed"), assertErrFake("enable failed"))

	require.Error(t, inst.Install(context.Background(), opts, &bytes.Buffer{}))
	assert.NotContains(t, callsSince(cmd, 0), "unmask cron.service")
}

// Re-running takeover finds cron masked by RunWisp itself; that must not be
// recorded as the operator's own mask, or uninstall would never bring cron back.
func TestTakeover_RerunKeepsTheOriginalCronState(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	opts := systemInstallOpts(binary)
	installTakeover(t, inst, fs, cmd, prompter, opts, "active\nenabled")

	for range 2 { // discovery, then the reassert probe: still retired, nothing to do
		cmd.Expect("systemctl", strings.Fields(cronShowState), []byte("loaded\ninactive\nmasked\n"), nil, nil)
	}
	opts.TakeOverCron = true
	require.NoError(t, inst.Install(context.Background(), opts, &bytes.Buffer{}))

	calls := uninstallSystem(t, inst, cmd)
	assert.Contains(t, calls, "unmask cron.service")
	assert.Contains(t, calls, "start cron.service")
}

const failsafePath = "/etc/systemd/system/runwisp-cron-failsafe.service"

// Finding RW-37#12: a take-over leaves cron masked, so a RunWisp that keeps
// crashing must eventually give up and hand the jobs back instead of
// restarting forever with no scheduler on the box.
func TestTakeover_InstallsCronFailsafe(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "active\nenabled")

	unit, err := fs.ReadFile(inst.unitPath(true))
	require.NoError(t, err)
	assert.Contains(t, string(unit), "StartLimitBurst=")
	assert.Contains(t, string(unit), "StartLimitIntervalSec=")
	assert.Contains(t, string(unit), "OnFailure=runwisp-cron-failsafe.service")
	failsafe, err := fs.ReadFile(failsafePath)
	require.NoError(t, err)
	assert.Contains(t, string(failsafe), ManagedMarker)

	uninstallSystem(t, inst, cmd)
	_, err = fs.ReadFile(failsafePath)
	assert.Error(t, err, "uninstall removes the failsafe with the unit")
}

// A plain install never masked cron, so it keeps systemd's default restart
// behaviour and gets no failsafe.
func TestInstall_NoTakeoverMeansNoFailsafe(t *testing.T) {
	inst, _, _, _, binary := newFakeInstaller(t, false)
	body, err := inst.Render(systemInstallOpts(binary))
	require.NoError(t, err)
	assert.NotContains(t, string(body), "OnFailure=")
	assert.NotContains(t, string(body), "StartLimitBurst=")
}

// Cron the operator masked themselves is not a scheduler to hand back to.
func TestTakeover_NoFailsafeWhenOperatorHadMaskedCron(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "inactive\nmasked")

	unit, err := fs.ReadFile(inst.unitPath(true))
	require.NoError(t, err)
	assert.NotContains(t, string(unit), "OnFailure=")
	_, err = fs.ReadFile(failsafePath)
	assert.Error(t, err)
}

// runFailsafe executes the failsafe unit's ExecStart script for real under
// /bin/sh, with a fake systemctl on PATH that reports state and result as
// runwisp.service's ActiveState and Result and logs every call. Returns the
// logged calls.
func runFailsafe(t *testing.T, failsafe []byte, state, result string) []string {
	t.Helper()
	var script string
	for _, l := range strings.Split(string(failsafe), "\n") {
		if v, ok := strings.CutPrefix(l, "ExecStart=/bin/sh -c '"); ok {
			script = strings.ReplaceAll(strings.TrimSuffix(v, "'"), "$$", "$")
		}
	}
	require.NotEmpty(t, script, "failsafe has no /bin/sh ExecStart")

	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	fake := "#!/bin/sh\necho \"$*\" >> " + logPath + "\n" +
		"case \"$3\" in ActiveState) echo " + state + ";; Result) echo " + result + ";; esac\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "systemctl"), []byte(fake), 0o755))

	c := exec.Command("/bin/sh", "-c", script)
	c.Env = []string{"PATH=" + dir}
	out, err := c.CombinedOutput()
	require.NoError(t, err, string(out))
	logged, _ := os.ReadFile(logPath)
	return strings.Split(strings.TrimSpace(string(logged)), "\n")
}

// OnFailure= fires on every crash while systemd is still restarting RunWisp,
// and on a stop that timed out. Only "failed" after the restarts ran out may
// bring cron back; a `runwisp stop` / `systemctl stop` must leave it masked.
// (A clean stop leaves the unit inactive, so OnFailure= never fires at all.)
// The states are the ones systemd 259 reports at each point.
func TestCronFailsafe_OnlyHandsBackAfterRestartsGiveUp(t *testing.T) {
	inst, fs, cmd, prompter, binary := newFakeInstaller(t, false)
	inst.deps.Euid = 0
	installTakeover(t, inst, fs, cmd, prompter, systemInstallOpts(binary), "active\nenabled")
	failsafe, err := fs.ReadFile(failsafePath)
	require.NoError(t, err)

	probes := []string{
		"show -p ActiveState --value runwisp.service",
		"show -p Result --value runwisp.service",
	}
	for _, gaveUp := range []string{"exit-code", "start-limit-hit", "signal"} {
		assert.Equal(t, append(probes, "unmask cron.service", "start --no-block cron.service"),
			runFailsafe(t, failsafe, "failed", gaveUp), gaveUp)
	}
	for _, stillTrying := range [][2]string{
		{"activating", "exit-code"}, // a crash systemd is about to restart
		{"failed", "timeout"},       // a stop that ran past TimeoutStopSec
	} {
		assert.Equal(t, probes, runFailsafe(t, failsafe, stillTrying[0], stillTrying[1]), stillTrying)
	}
}
