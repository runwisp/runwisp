// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart/autostartfake"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart/autostarttest"
	"github.com/runwisp/runwisp/apps/runwisp/internal/bootid"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cutover"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
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

// newTakeoverInstaller is a fake init system on a box with cron running and no
// RunWisp unit yet: its plan is an install, so a cutover has something to do.
func newTakeoverInstaller() *autostartfake.Installer {
	return &autostartfake.Installer{
		CronUnit: "cron.service", CronActive: true,
		Plan: autostart.Plan{Kind: autostart.PlanInstall, Steps: []autostart.Step{
			{Description: "write /etc/systemd/system/runwisp.service"},
		}},
	}
}

// takeoverHarness substitutes the whole machine behind the newTakeover seam: a
// fake init system, a crontab in a temp dir, and a config path that starts out
// missing — the box the reported dead-end was about.
type takeoverHarness struct {
	inst *autostartfake.Installer

	cfgPath string
	dataDir string

	reloads int
	answer  bool
	euid    int
	// noCrontabs describes a box with nothing to take over.
	noCrontabs bool
	// configBody, when non-empty, is written to cfgPath before the run.
	configBody string
	// binaryProblem makes the plan refuse the unit's binary path.
	binaryProblem error

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
	h.inst = newTakeoverInstaller()

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
			Prompter:  &autostarttest.ScriptedPrompter{YesNo: []bool{h.answer}},
			Opts: autostart.InstallOptions{
				Binary: "/usr/local/bin/runwisp", Config: h.cfgPath,
				DataDir: dir, Host: "127.0.0.1", Port: 9477, System: true,
			},
			GOOS:          "linux",
			SystemdBooted: func() bool { return true },
			Euid:          h.euid,
			Scan: func(_ []string, cfgPath string) config.CronScan {
				return config.ScanCronSources([]string{filepath.Join(crontabs, "*")}, cfgPath)
			},
			Trusted:       func(string) error { return nil },
			BinaryProblem: h.binaryProblem,
			// The seam under test: fed by this package's real PID-file probe, so
			// the sampling order is exercised end to end rather than assumed.
			Preflight:     func(context.Context) (bool, error) { return false, nil },
			DaemonRunning: func() bool { return isDaemonRunning(f) },
			Reload:        func(context.Context) error { h.reloads++; return nil },
		}), nil
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

// writeLivePidFile claims a data dir for a live daemon (it holds the PID-file
// lock for the rest of the test), so isDaemonRunning reports true without a
// real one.
func writeLivePidFile(t *testing.T, dir string) {
	t.Helper()
	lock, err := datadir.AcquireDaemonLock(dir)
	require.NoError(t, err)
	t.Cleanup(lock.Release)
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
	assert.Contains(t, out, "Dashboard:  http://127.0.0.1:9477")
	assert.Contains(t, out, "Password:   sudo runwisp password",
		"the system daemon's socket is root's, so the way in has to be spelled out")
	assert.FileExists(t, h.cfgPath)
	assert.Equal(t, 1, h.inst.Installs())
}

// The whole reason `takeover` exists as its own command rather than an alias:
// masking cron does not change a running daemon's view of the world, and
// `systemctl enable --now` on an already-active unit is a no-op, so without this
// reload the jobs stay held until the operator works out they must reload.
func TestRunTakeover_ReloadsRunningDaemonAfterInstall(t *testing.T) {
	h := newTakeoverHarness(t)
	writeLivePidFile(t, h.dataDir)

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.Installs())
	assert.Equal(t, 1, h.reloads, "a running daemon must be reloaded so the hold lifts")
	assert.Contains(t, h.out.String(), "Reloading the running daemon")
}

// The daemon systemd just started loaded its config after cron was masked, so
// nothing is held — a reload would be noise, and on the first-install path there
// may be no socket to reload over at all yet. The decision is therefore sampled
// before the install, which this test pins by having the install start one.
func TestRunTakeover_NoReloadWhenTheInstallItselfStartedTheDaemon(t *testing.T) {
	h := newTakeoverHarness(t)
	// Install leaves a live PID file behind, the way `systemctl enable --now`
	// leaves a daemon behind.
	h.inst.OnInstall = func(autostart.InstallOptions) { writeLivePidFile(t, h.dataDir) }

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.Installs())
	assert.Zero(t, h.reloads, "the daemon systemd just started has nothing held to hand over")
	assert.NotContains(t, h.out.String(), "Reloading")
}

func TestRunTakeover_NoReloadWhenNoDaemonWasRunning(t *testing.T) {
	h := newTakeoverHarness(t)

	require.NoError(t, h.run(t, h.flags()))

	assert.Equal(t, 1, h.inst.Installs())
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
	assert.Zero(t, h.inst.Installs())
	assert.Zero(t, h.reloads)
}

// TestRunTakeover_DryRunWithUndurableBinaryPrintsTheBlockerThenFails: a binary
// under /tmp used to abort before any plan existed. The dry run says what it
// found, lists the binary as a blocker, and exits non-zero so a script cannot
// read it as ready. It prints no steps: a plan that won't run has none.
func TestRunTakeover_DryRunWithUndurableBinaryPrintsTheBlockerThenFails(t *testing.T) {
	h := newTakeoverHarness(t)
	h.binaryProblem = errors.New("refusing to install service pointing at /tmp/runwisp")
	takeoverOpts.DryRun = true

	err := h.run(t, h.flags())
	require.Error(t, err)

	assert.Contains(t, h.out.String(), "Found ")
	assert.NotContains(t, h.out.String(), "Write "+h.cfgPath)
	assert.Contains(t, err.Error(), "binary path is not durable")
	assert.Contains(t, err.Error(), "/tmp/runwisp")
	assert.Zero(t, h.inst.Installs())
}

// TestResolveServiceOptionsTolerant_ReportsAnUndurableBinary: a real install still
// refuses a binary under /tmp, while the tolerant form hands the refusal back with
// the rest of the options so a plan can be built around it.
func TestResolveServiceOptionsTolerant_ReportsAnUndurableBinary(t *testing.T) {
	restoreInstallOpts(t)
	dir := durableTempDir(t)
	tomlPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(tomlPath, []byte("[daemon]\n"), 0o600))
	f := Flags{CfgFile: tomlPath, DataDir: dir}
	cmd := installFlagsCmd(t, f, &bytes.Buffer{}, &bytes.Buffer{})
	require.NoError(t, cmd.Flags().Set("data", dir))
	require.NoError(t, cmd.Flags().Set("config", tomlPath))
	deps := autostart.Deps{Home: t.TempDir(), User: "tester", Fingerprint: "fp", Prompter: &autostarttest.ScriptedPrompter{}}
	// A literal /tmp path, not os.TempDir(): on macOS that is $TMPDIR under
	// /var/folders, which ResolveBinary does not refuse. An explicit --binary skips
	// the stat, so the file need not exist.
	binary := "/tmp/runwisp-undurable"

	_, err := resolveServiceOptions(cmd, deps, f, false, binary)
	require.Error(t, err)

	opts, binaryErr, err := resolveServiceOptionsTolerant(cmd, deps, f, false, binary, true)
	require.NoError(t, err)
	require.Error(t, binaryErr)
	assert.Equal(t, binary, opts.Binary)
	assert.Equal(t, tomlPath, opts.Config)
	assert.Contains(t, binaryProblem(binaryErr).Error(), "refusing to install service pointing at "+binary)
}

// A declined prompt is not a failure: nothing has been written yet, so the box is
// exactly as it was.
func TestRunTakeover_DeclinedWritesNothing(t *testing.T) {
	h := newTakeoverHarness(t)
	h.answer = false

	require.NoError(t, h.run(t, h.flags()))

	assert.Contains(t, h.out.String(), "Aborted")
	assert.NoFileExists(t, h.cfgPath)
	assert.Zero(t, h.inst.Installs())
}

// TestRunTakeover_BlockedPlanReportsEveryBlockerAndExitsNonZero: every blocker
// is reported, not just the first. With no cron jobs to show, the blockers are
// the whole output.
func TestRunTakeover_BlockedPlanReportsEveryBlockerAndExitsNonZero(t *testing.T) {
	h := newTakeoverHarness(t)
	h.noCrontabs = true
	h.configBody = "this is not toml"

	err := h.run(t, h.flags())
	require.Error(t, err)

	assert.Empty(t, h.out.String())
	assert.Contains(t, err.Error(), "2 things stop RunWisp taking over cron")
	assert.Contains(t, err.Error(), "does not load")
	assert.Contains(t, err.Error(), "no cron jobs on this box")
	assert.Zero(t, h.inst.Installs())

	_, ok := isUserFacing(err)
	assert.True(t, ok, "a refusal renders through the CLI's pretty path")
}

// A single blocker is reported as itself rather than wrapped in a count.
func TestRunTakeover_SingleBlockerKeepsItsOwnTitle(t *testing.T) {
	h := newTakeoverHarness(t)
	h.euid = 1000

	err := h.run(t, h.flags())
	require.Error(t, err)

	assert.Contains(t, err.Error(), "taking over cron needs root")
	assert.NotContains(t, err.Error(), "things stop")
	assert.Empty(t, h.out.String(), "a non-root run saw nothing worth printing")
}

// Re-running a finished take-over must be a no-op, so it is safe in a
// provisioning script.
func TestRunTakeover_NothingToDoIsANoop(t *testing.T) {
	h := newTakeoverHarness(t)
	h.inst.Plan = autostart.Plan{Kind: autostart.PlanNoop}
	h.inst.Stat = autostart.Status{Installed: true, Autostart: true, Running: true}
	h.inst.CronActive = false
	dir := filepath.Dir(h.cfgPath)
	h.configBody = "[daemon]\ninclude_cron = [\"" + filepath.Join(dir, "crontabs", "*") + "\"]\n"

	require.NoError(t, h.run(t, h.flags()))

	assert.Contains(t, h.out.String(), "Nothing to do")
	assert.Zero(t, h.inst.Installs())
	assert.Zero(t, h.reloads)
}

// A take-over saved the service's password after starting it and then
// restarted it, so the service started twice in one second and killed the
// @reboot run the first start fired. The password now goes to the installer,
// which saves it before the first start, and nothing restarts afterwards.
func TestTakeoverSavesThePasswordBeforeTheFirstStart(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "")
	t.Setenv("RUNWISP_PASSWORD", "")
	dir := t.TempDir()
	tab := filepath.Join(dir, "crontabs", "backup")
	require.NoError(t, os.MkdirAll(filepath.Dir(tab), 0o755))
	require.NoError(t, os.WriteFile(tab, []byte("17 3 * * * /usr/bin/backup\n"), 0o644))
	inst := newTakeoverInstaller()
	opts := autostart.InstallOptions{
		Binary: "/usr/local/bin/runwisp", Config: filepath.Join(dir, "runwisp.toml"),
		DataDir: dir, Host: "127.0.0.1", Port: 9477, System: true,
	}
	c := newCutover(Flags{}, autostart.Deps{Prompter: &autostarttest.ScriptedPrompter{}}, inst, opts, func(d *cutover.Deps) {
		d.GOOS = "linux"
		d.SystemdBooted = func() bool { return true }
		d.Scan = func(_ []string, cfgPath string) config.CronScan {
			return config.ScanCronSources([]string{filepath.Join(dir, "crontabs", "*")}, cfgPath)
		}
		d.Trusted = func(string) error { return nil }
		d.Preflight = func(context.Context) (bool, error) { return false, nil }
		d.DaemonRunning = func() bool { return false }
		d.MarkBootRan = nil
	})

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	_, err = c.Execute(context.Background(), p, &bytes.Buffer{})
	require.NoError(t, err)
	assert.NotEmpty(t, inst.InstallOpts.Password, "the installer saves it before the first start")
	assert.Zero(t, inst.Restarts, "nothing restarts the service after it started")
}

// Cron runs @reboot lines when the machine boots, and RunWisp's first start
// right after a take-over ran them again. The take-over now records them as
// run this boot in the data dir the service opens, so that start skips them.
func TestMarkCronBootTasksRan_RecordsTheRebootLines(t *testing.T) {
	dir := t.TempDir()
	tab := filepath.Join(dir, "crontabs", "ops")
	require.NoError(t, os.MkdirAll(filepath.Dir(tab), 0o755))
	require.NoError(t, os.WriteFile(tab, []byte("@reboot /usr/bin/start-app\n17 3 * * * /usr/bin/backup\n"), 0o644))
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath,
		[]byte("[daemon]\ninclude_cron = [\""+filepath.Join(dir, "crontabs", "*")+"\"]\n"), 0o644))
	data := filepath.Join(dir, "data")

	require.NoError(t, markCronBootTasksRan(cfgPath, data))

	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	names := cronBootTasks(cfg)
	require.Len(t, names, 1, "only the @reboot line")
	db, err := storage.New(datadir.DBPath(data))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	got, err := db.GetTaskBootID(t.Context(), names[0])
	require.NoError(t, err)
	assert.Equal(t, bootid.Current(), got)
}

// Over SSH the loopback dashboard URL is useless on its own; the summary adds
// the tunnel that reaches it.
func TestPrintTakeoverDone_SSHTunnel(t *testing.T) {
	cfg := Flags{CfgFile: filepath.Join(t.TempDir(), "none.toml"), Port: 9477}
	t.Setenv("SUDO_USER", "ops")

	t.Setenv("SSH_CONNECTION", "")
	var out bytes.Buffer
	printTakeoverDone(&out, cfg, "/usr/local/bin/runwisp")
	assert.NotContains(t, out.String(), "Tunnel:")

	t.Setenv("SSH_CONNECTION", "203.0.113.9 51234 198.51.100.7 22")
	out.Reset()
	printTakeoverDone(&out, cfg, "/usr/local/bin/runwisp")
	assert.Contains(t, out.String(), "Tunnel:     ssh -L 9477:127.0.0.1:9477 ops@198.51.100.7\n")
}

// The unit runs the root-owned copy service install made, and `upgrade`
// replaces the binary it runs from, so the hint must name that copy rather
// than the home binary the operator typed takeover with.
func TestPrintTakeoverDone_UpgradeNamesTheUnitBinary(t *testing.T) {
	cfg := Flags{CfgFile: filepath.Join(t.TempDir(), "none.toml")}

	var out bytes.Buffer
	printTakeoverDone(&out, cfg, "/usr/local/bin/runwisp")
	assert.Contains(t, out.String(), "Upgrade:    sudo runwisp upgrade\n")
	assert.Contains(t, out.String(), "Reload:     sudo runwisp reload (after crontab -e)\n")

	out.Reset()
	printTakeoverDone(&out, cfg, "/opt/rw/runwisp")
	assert.Contains(t, out.String(), "Upgrade:    sudo /opt/rw/runwisp upgrade\n")
}
