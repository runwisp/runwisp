// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cutover"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests describe the command, not the decision: whether a take-over is
// legal, and what its steps are, is internal/cutover's business and is tested
// there against injected evidence. What is left here is the wiring — that the
// plan is printed before anything happens, that --dry-run writes nothing, that a
// blocked plan exits non-zero, and that the reload seam is fed by this package's
// real isDaemonRunning.

// fakeTakeoverInstaller is an autostart.Installer that records what it was asked
// to do. Only the methods a cutover reaches are meaningful.
type fakeTakeoverInstaller struct {
	cronUnit   string
	cronActive bool
	installed  bool

	// startsDaemon makes Install leave a live PID file behind, the way
	// `systemctl enable --now` leaves a daemon behind.
	startsDaemon bool
	dataDir      string
	t            *testing.T

	installs int
	// configAtInstall records whether opts.Config existed when Install ran — the
	// ordering a unit pointing at a missing config would break.
	configAtInstall bool
}

func (f *fakeTakeoverInstaller) Render(autostart.InstallOptions) ([]byte, error) { return nil, nil }

func (f *fakeTakeoverInstaller) ComputePlan(context.Context, autostart.InstallOptions) (autostart.Plan, error) {
	if f.installed {
		return autostart.Plan{Kind: autostart.PlanNoop}, nil
	}
	return autostart.Plan{Kind: autostart.PlanInstall, Steps: []autostart.Step{
		{Action: autostart.ActionWriteUnit, Description: "write /etc/systemd/system/runwisp.service"},
	}}, nil
}

func (f *fakeTakeoverInstaller) Install(_ context.Context, opts autostart.InstallOptions, _ io.Writer) error {
	f.installs++
	_, err := os.Stat(opts.Config)
	f.configAtInstall = err == nil
	if f.startsDaemon {
		writeLivePidFile(f.t, f.dataDir)
	}
	return nil
}

func (f *fakeTakeoverInstaller) ComputeUninstallPlan(context.Context, autostart.UninstallOptions) (autostart.Plan, error) {
	return autostart.Plan{}, nil
}

func (f *fakeTakeoverInstaller) Uninstall(context.Context, autostart.UninstallOptions, io.Writer) error {
	return nil
}

func (f *fakeTakeoverInstaller) Status(context.Context, autostart.InstallOptions) (autostart.Status, error) {
	return autostart.Status{Installed: f.installed}, nil
}

func (f *fakeTakeoverInstaller) Stop(context.Context, autostart.InstallOptions) error    { return nil }
func (f *fakeTakeoverInstaller) Restart(context.Context, autostart.InstallOptions) error { return nil }

func (f *fakeTakeoverInstaller) CronStatus(context.Context) (string, bool, error) {
	return f.cronUnit, f.cronActive, nil
}

// takeoverHarness substitutes the whole machine behind the newTakeover seam: a
// fake init system, a crontab in a temp dir, and a config path that starts out
// missing — the box the reported dead-end was about.
type takeoverHarness struct {
	inst *fakeTakeoverInstaller

	cfgPath string
	dataDir string

	reloads int
	answer  bool
	euid    int
	// noCrontabs describes a box with nothing to take over.
	noCrontabs bool
	// configBody, when non-empty, is written to cfgPath before the run.
	configBody string

	out bytes.Buffer
}

func newTakeoverHarness(t *testing.T) *takeoverHarness {
	t.Helper()
	dir := t.TempDir()
	h := &takeoverHarness{
		cfgPath: filepath.Join(dir, "runwisp.toml"),
		dataDir: dir,
		answer:  true,
	}
	h.inst = &fakeTakeoverInstaller{
		cronUnit: "cron.service", cronActive: true, dataDir: dir, t: t,
	}

	prevSeam, prevOpts := newTakeover, takeoverOpts
	t.Cleanup(func() { newTakeover, takeoverOpts = prevSeam, prevOpts })

	newTakeover = func(_ *cobra.Command, f Flags, _ takeoverRequest) (*cutover.Cutover, error) {
		if h.configBody != "" {
			require.NoError(t, os.WriteFile(h.cfgPath, []byte(h.configBody), 0o644))
		}
		crontabs := filepath.Join(dir, "crontabs")
		require.NoError(t, os.MkdirAll(crontabs, 0o755))
		if !h.noCrontabs {
			require.NoError(t, os.WriteFile(filepath.Join(crontabs, "backup"),
				[]byte("17 3 * * * /usr/bin/backup\n"), 0o644))
		}

		return cutover.New(cutover.Deps{
			Installer: h.inst,
			Prompter:  &autostart.ScriptedPrompter{YesNo: []bool{h.answer}},
			Opts: autostart.InstallOptions{
				Binary: "/usr/local/bin/runwisp", Config: h.cfgPath,
				DataDir: dir, Host: "127.0.0.1", Port: 9477, System: true,
			},
			GOOS: "linux",
			Euid: h.euid,
			Scan: func(_ []string, cfgPath string) config.CronScan {
				return config.ScanCronSources([]string{filepath.Join(crontabs, "*")}, cfgPath)
			},
			Trusted: func(string) error { return nil },
			// The seam under test: fed by this package's real PID-file probe, so
			// the sampling order is exercised end to end rather than assumed.
			DaemonRunning: func() bool { return isDaemonRunning(f) },
			Reload:        func() error { h.reloads++; return nil },
		}, cutover.Options{}), nil
	}
	return h
}

func (h *takeoverHarness) run(t *testing.T, f Flags) error {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&h.out)
	cmd.SetErr(&bytes.Buffer{})
	return runTakeover(cmd, f)
}

func (h *takeoverHarness) flags() Flags {
	return Flags{CfgFile: h.cfgPath, DataDir: h.dataDir, Host: "127.0.0.1", Port: 9477}
}

// writeLivePidFile claims a data dir for a live daemon, so isDaemonRunning
// reports true without a real one.
func writeLivePidFile(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "daemon.pid"),
		[]byte(strconv.Itoa(os.Getpid())), 0o600))
}

// TestRunTakeover_WorksFromNothing is the regression test at the command level:
// a box with cron jobs and no runwisp.toml used to be told to go author one.
func TestRunTakeover_WorksFromNothing(t *testing.T) {
	h := newTakeoverHarness(t)

	require.NoError(t, h.run(t, h.flags()))

	out := h.out.String()
	assert.Contains(t, out, "Found 1 cron job on this box:")
	assert.Contains(t, out, "Write "+h.cfgPath)
	assert.Contains(t, out, "write /etc/systemd/system/runwisp.service",
		"autostart's own steps are shown, not a restatement of them")
	assert.FileExists(t, h.cfgPath)
	assert.Equal(t, 1, h.inst.installs)
}

// The whole reason `takeover` exists as its own command rather than an alias:
// masking cron does not change a running daemon's view of the world, and
// `systemctl enable --now` on an already-active unit is a no-op, so without this
// reload the jobs stay held until the operator works out they must reload.
func TestRunTakeover_ReloadsRunningDaemonAfterInstall(t *testing.T) {
	h := newTakeoverHarness(t)
	writeLivePidFile(t, h.dataDir)

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.installs)
	assert.Equal(t, 1, h.reloads, "a running daemon must be reloaded so the hold lifts")
	assert.Contains(t, h.out.String(), "Reloading the running daemon")
}

// The daemon systemd just started loaded its config after cron was masked, so
// nothing is held — a reload would be noise, and on the first-install path there
// may be no socket to reload over at all yet. The decision is therefore sampled
// before the install, which this test pins by having the install start one.
func TestRunTakeover_NoReloadWhenTheInstallItselfStartedTheDaemon(t *testing.T) {
	h := newTakeoverHarness(t)
	h.inst.startsDaemon = true

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.installs)
	assert.Zero(t, h.reloads, "the daemon systemd just started has nothing held to hand over")
	assert.NotContains(t, h.out.String(), "Reloading")
}

func TestRunTakeover_NoReloadWhenNoDaemonWasRunning(t *testing.T) {
	h := newTakeoverHarness(t)

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.installs)
	assert.Zero(t, h.reloads)
}

// TestRunTakeover_DryRunPrintsThePlanAndWritesNothing is the other half of the
// reported bug: --dry-run used to exit with the cron gate's error on exactly the
// box that most needed to see a plan.
func TestRunTakeover_DryRunPrintsThePlanAndWritesNothing(t *testing.T) {
	h := newTakeoverHarness(t)
	takeoverOpts.DryRun = true

	require.NoError(t, h.run(t, h.flags()))

	out := h.out.String()
	assert.Contains(t, out, "Write "+h.cfgPath)
	assert.Contains(t, out, "Dry run — nothing was written")
	assert.NoFileExists(t, h.cfgPath)
	assert.Zero(t, h.inst.installs)
	assert.Zero(t, h.reloads)
}

// A declined prompt is not a failure: nothing has been written yet, so the box is
// exactly as it was.
func TestRunTakeover_DeclinedWritesNothing(t *testing.T) {
	h := newTakeoverHarness(t)
	h.answer = false

	require.NoError(t, h.run(t, h.flags()))

	assert.Contains(t, h.out.String(), "Aborted")
	assert.NoFileExists(t, h.cfgPath)
	assert.Zero(t, h.inst.installs)
}

// TestRunTakeover_BlockedPlanPrintsFindingsThenExitsNonZero: the plan block still
// prints — someone told "this needs root" wants to know jobs are waiting — and
// every blocker is reported, not just the first.
func TestRunTakeover_BlockedPlanPrintsFindingsThenExitsNonZero(t *testing.T) {
	h := newTakeoverHarness(t)
	h.euid = 1000
	h.noCrontabs = true

	err := h.run(t, h.flags())
	require.Error(t, err)

	assert.Contains(t, h.out.String(), "No cron jobs found on this box.")
	assert.Contains(t, err.Error(), "2 things stop RunWisp taking over cron")
	assert.Contains(t, err.Error(), "root")
	assert.Contains(t, err.Error(), "no cron jobs on this box")
	assert.Zero(t, h.inst.installs)

	_, ok := isUserFacing(err)
	assert.True(t, ok, "a refusal renders through the CLI's pretty path")
}

// A single blocker is reported as itself rather than wrapped in a count.
func TestRunTakeover_SingleBlockerKeepsItsOwnTitle(t *testing.T) {
	h := newTakeoverHarness(t)
	h.euid = 1000

	err := h.run(t, h.flags())
	require.Error(t, err)

	assert.Contains(t, err.Error(), "taking over cron requires root")
	assert.NotContains(t, err.Error(), "things stop")
}

// Re-running a finished take-over must be a no-op, so it is safe in a
// provisioning script.
func TestRunTakeover_NothingToDoIsANoop(t *testing.T) {
	h := newTakeoverHarness(t)
	h.inst.installed = true
	h.inst.cronActive = false
	dir := filepath.Dir(h.cfgPath)
	h.configBody = "[daemon]\ninclude_cron = [\"" + filepath.Join(dir, "crontabs", "*") + "\"]\n"

	require.NoError(t, h.run(t, h.flags()))

	assert.Contains(t, h.out.String(), "Nothing to do")
	assert.Zero(t, h.inst.installs)
	assert.Zero(t, h.reloads)
}
