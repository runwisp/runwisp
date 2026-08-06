// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// oneJob is a crontab RunWisp can read and reproduce.
const oneJob = "17 3 * * * /usr/bin/backup\n"

// skippedJob is a readable crontab holding one job RunWisp will not schedule:
// crond pipes the text after the '%' to the command on stdin, which no TOML task
// expresses, so the job is read and deliberately left unscheduled. The good job
// beside it keeps the file itself parseable — this is a per-job skip, not a
// whole-source refusal.
const skippedJob = "17 3 * * * /usr/bin/backup\n0 5 * * * /usr/bin/wall %the box is going down\n"

// writeWired is the WireCron fake: it appends include_cron the way the real
// configedit.WireCronInclude would, so the loader sees a config that reads the
// crontabs.
func writeWired(path string, patterns []string) error {
	orig, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(orig, []byte("\n"+config.CronIncludeArray(patterns))...), 0o644)
}

// TestCompute_MissingConfigIsAStepNotARefusal is the regression test for the bug
// that started this: a box with cron jobs and no runwisp.toml used to be told to
// go author one. Writing it is now step one.
func TestCompute_MissingConfigIsAStepNotARefusal(t *testing.T) {
	c, _, _ := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	assert.False(t, p.Blocked(), "a bare box with cron jobs must not be blocked: %v", blockerKinds(p))
	assert.Equal(t, []StepKind{StepWriteConfig, StepInstallService}, stepKinds(p))
	assert.True(t, p.pending(StepWriteConfig))
	assert.True(t, p.MasksCron)
}

func TestCompute_ExistingConfigWithoutIncludeCronGetsWired(t *testing.T) {
	c, _, _ := fixture{
		config:     "[tasks.hello]\nrun = \"echo hi\"\n",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	assert.False(t, p.Blocked(), "%v", blockerKinds(p))
	assert.Equal(t, []StepKind{StepWireIncludeCron, StepInstallService}, stepKinds(p))
	assert.True(t, p.pending(StepWireIncludeCron))
}

func TestCompute_ConfigAlreadyReadingCrontabsMarksTheStepDone(t *testing.T) {
	c, _, cfgPath := fixture{
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	// Wire it by hand first, so the config genuinely reads the crontab.
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"[daemon]\ninclude_cron = [\""+filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.False(t, p.Blocked(), "%v", blockerKinds(p))
	wire, ok := p.step(StepWireIncludeCron)
	require.True(t, ok)
	assert.True(t, wire.Satisfied, "a config that already reads the crontabs needs no edit")
	assert.NotEmpty(t, p.Evidence.ReadFiles)
}

// TestCompute_OperatorsOwnIncludeCronIsNeverRewritten pins the one place the
// feature still says no. An include_cron the operator maintains is theirs; RunWisp
// prints what to add rather than editing the array.
func TestCompute_OperatorsOwnIncludeCronIsNeverRewritten(t *testing.T) {
	c, _, _ := fixture{
		config:     "[daemon]\ninclude_cron = [\"/nowhere/*\"]\n",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.True(t, p.Blocked())
	assert.True(t, hasBlocker(p, BlockerIncludeCronMissesCrontabs), "%v", blockerKinds(p))

	b, _ := p.FirstBlocker()
	assert.Contains(t, b.Details, "include_cron = [", "it has to print the array to paste")
	assert.Contains(t, b.Details, "crontabs")
}

func TestCompute_NoCronJobsAnywhereIsTheOnlyNothingToReadRefusal(t *testing.T) {
	c, _, _ := fixture{cronUnit: "cron.service", cronActive: true}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.True(t, p.Blocked())
	assert.True(t, hasBlocker(p, BlockerNoCronJobs), "%v", blockerKinds(p))

	b, _ := p.FirstBlocker()
	assert.Contains(t, b.Title, "no cron jobs on this box")
	assert.NotContains(t, b.Details, "Add include_cron first",
		"the old wording sent operators off to do RunWisp's job for it")
}

func TestCompute_BrokenConfigBlocksAndSaysNothingWasWritten(t *testing.T) {
	c, _, _ := fixture{
		config:     "[daemon\nthis is not toml",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err, "a broken config is a blocker, not a Compute error")

	require.True(t, p.Blocked())
	assert.True(t, hasBlocker(p, BlockerConfigUnloadable), "%v", blockerKinds(p))
	b, _ := p.FirstBlocker()
	assert.Contains(t, b.Details, "cron is untouched")
}

func TestCompute_NonRootIsBlockedEvenWithJobsToTakeOver(t *testing.T) {
	c, _, _ := fixture{
		euid:       1000,
		username:   "someone",
		crontabs:   map[string]string{"backup": oneJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	assert.True(t, hasBlocker(p, BlockerNeedsRoot), "%v", blockerKinds(p))
}

// TestCompute_DarwinRefusesUpFrontWithTheManualRoute runs on whatever platform
// the suite runs on, which is the point: the old code needed a cronTakeoverGOOS
// package var to fake this, and the launchd scope error ("re-run with --local")
// used to leak through as the answer instead.
func TestCompute_DarwinRefusesUpFrontWithTheManualRoute(t *testing.T) {
	c, inst, _ := fixture{
		goos:     "darwin",
		crontabs: map[string]string{"backup": oneJob},
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.Equal(t, []BlockerKind{BlockerNotLinux}, blockerKinds(p),
		"nothing below the OS can matter on a host that cannot mask cron")
	b, _ := p.FirstBlocker()
	assert.Contains(t, b.Details, "runwisp import cron --write")
	assert.Contains(t, b.Details, "crontab -r")
	assert.Contains(t, b.Details, "SIP-protected")
	assert.NotContains(t, b.Details, "--local")
	assert.Empty(t, inst.calls, "no init-system call should happen on an unsupported host")
}

// TestCompute_NoCronUnitIsNotABlocker covers a Docker/sysvinit/openrc box.
// autostart's discoverCronUnit errors when asked to mask a unit that doesn't
// exist, so passing TakeOverCron there would fail an install the operator
// genuinely wants — and those jobs are not held either, since deriving a hold
// needs systemctl or a cron pidfile.
func TestCompute_NoCronUnitIsNotABlocker(t *testing.T) {
	c, _, _ := fixture{crontabs: map[string]string{"backup": oneJob}}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	assert.False(t, p.Blocked(), "%v", blockerKinds(p))
	assert.False(t, p.MasksCron, "there is no unit to mask")
	assert.True(t, p.pending(StepInstallService))
}

func TestCompute_SkippedCronSourcesBlockUnlessAllowed(t *testing.T) {
	fx := fixture{
		crontabs:    map[string]string{"backup": oneJob},
		scanBlocked: []string{"/etc/cron.d/web: not owned by root"},
		cronUnit:    "cron.service",
		cronActive:  true,
	}

	c, _, _ := fx.build(t)
	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, hasBlocker(p, BlockerCronSourcesFailed), "%v", blockerKinds(p))

	fx.allowSkipped = true
	c, _, _ = fx.build(t)
	p, err = c.Compute(context.Background())
	require.NoError(t, err)
	assert.False(t, hasBlocker(p, BlockerCronSourcesFailed), "the override must clear it")
}

// TestCompute_SkippedJobBlocksWithNoConfigOnDisk is the first half of the gate
// inconsistency: a job that won't run inside an otherwise-readable crontab was
// only ever read off a loaded config, so the documented starting point for
// `takeover` — a box with no runwisp.toml — sailed through with no override
// asked for at all.
func TestCompute_SkippedJobBlocksWithNoConfigOnDisk(t *testing.T) {
	fx := fixture{
		crontabs:   map[string]string{"backup": skippedJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}

	c, _, cfgPath := fx.build(t)
	_, err := os.Stat(cfgPath)
	require.Error(t, err, "this test is about the no-config case")

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.True(t, hasBlocker(p, BlockerCronSourcesFailed), "%v", blockerKinds(p))

	fx.allowSkipped = true
	c, _, _ = fx.build(t)
	p, err = c.Compute(context.Background())
	require.NoError(t, err)
	assert.False(t, hasBlocker(p, BlockerCronSourcesFailed), "the override must clear it")
}

// TestCompute_SameBoxTwiceAgreesOnTheCronGate is the regression test for the
// inconsistency itself, whichever per-job failure triggers it: `takeover`
// promises a finished run is a safe no-op, and it used to ask for nothing on the
// first pass and then hard-block the identical box on the second — the only thing
// that had changed being the runwisp.toml RunWisp itself wrote.
func TestCompute_SameBoxTwiceAgreesOnTheCronGate(t *testing.T) {
	fx := fixture{
		crontabs:   map[string]string{"backup": skippedJob},
		cronUnit:   "cron.service",
		cronActive: true,
	}
	c, _, cfgPath := fx.build(t)

	first, err := c.Compute(context.Background())
	require.NoError(t, err)

	// Write the config the way Execute's StepWriteConfig would, then plan again
	// against a box that is otherwise byte-identical.
	require.NoError(t, c.deps.WriteConfig(cfgPath, first.Evidence.Patterns))
	second, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.Equal(t, blockerKinds(first), blockerKinds(second),
		"the gate must not depend on whether runwisp.toml exists yet")
	firstBlocker, ok := first.FirstBlocker()
	require.True(t, ok, "%v", blockerKinds(first))
	secondBlocker, _ := second.FirstBlocker()
	assert.Equal(t, firstBlocker, secondBlocker, "the same skip must not be counted twice")

	// And the override clears it on both passes, not just the second.
	fx.allowSkipped = true
	c, _, cfgPath = fx.build(t)
	bare, err := c.Compute(context.Background())
	require.NoError(t, err)
	require.NoError(t, c.deps.WriteConfig(cfgPath, bare.Evidence.Patterns))
	wired, err := c.Compute(context.Background())
	require.NoError(t, err)
	assert.False(t, hasBlocker(bare, BlockerCronSourcesFailed))
	assert.False(t, hasBlocker(wired, BlockerCronSourcesFailed))
}

func TestCompute_UntrustedConfigIsABlockerSoDryRunCanReportIt(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[tasks.a]\nrun = \"true\"\n"), 0o644))
	cronDir := filepath.Join(dir, "crontabs")
	require.NoError(t, os.MkdirAll(cronDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(cronDir, "backup"), []byte(oneJob), 0o644))

	inst := &fakeInstaller{cronUnit: "cron.service", cronActive: true, plan: autostart.Plan{Kind: autostart.PlanInstall}}
	c := New(Deps{
		Installer: inst,
		Prompter:  &autostart.ScriptedPrompter{},
		Opts:      autostart.InstallOptions{Config: cfgPath, System: true, Port: 9477},
		GOOS:      "linux",
		Scan: func(_ []string, p string) config.CronScan {
			return config.ScanCronSources([]string{filepath.Join(cronDir, "*")}, p)
		},
		Trusted:       func(string) error { return assert.AnError },
		DaemonRunning: func() bool { return false },
	}, Options{})

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	assert.True(t, hasBlocker(p, BlockerConfigUntrusted), "%v", blockerKinds(p))
}

// TestCompute_ReloadStepOnlyWhenADaemonWasAlreadyRunning pins the invariant the
// old cmd_takeover.go tests protected: the sample happens before anything is
// installed, because `systemctl enable --now` leaves a daemon behind and a check
// made afterwards would reload a socket that isn't accepting yet.
func TestCompute_ReloadStepOnlyWhenADaemonWasAlreadyRunning(t *testing.T) {
	for _, running := range []bool{true, false} {
		c, _, _ := fixture{
			crontabs:      map[string]string{"backup": oneJob},
			cronUnit:      "cron.service",
			cronActive:    true,
			daemonRunning: running,
		}.build(t)

		p, err := c.Compute(context.Background())
		require.NoError(t, err)
		_, has := p.step(StepReloadRunningDaemon)
		assert.Equal(t, running, has, "daemonRunning=%v", running)
	}
}

// TestCompute_CronBackAfterATakeoverIsNotSatisfied is the repair case: the unit
// on disk already matches so autostart calls it a no-op, but cron is active
// again. Treating that as done is how a re-run used to print "Already installed
// ✓" and return on a box firing every job twice.
func TestCompute_CronBackAfterATakeoverIsNotSatisfied(t *testing.T) {
	c, _, cfgPath := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		cronUnit:      "cron.service",
		cronActive:    true,
		unitInstalled: true,
	}.build(t)
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"[daemon]\ninclude_cron = [\""+filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.False(t, p.Blocked(), "%v", blockerKinds(p))
	assert.True(t, p.pending(StepInstallService), "cron is back — there is work to do")
	assert.False(t, p.NothingToDo())
}

func TestCompute_FullyDoneBoxHasNothingToDo(t *testing.T) {
	c, _, cfgPath := fixture{
		crontabs:      map[string]string{"backup": oneJob},
		cronUnit:      "cron.service",
		cronActive:    false, // already masked
		unitInstalled: true,
	}.build(t)
	require.NoError(t, os.WriteFile(cfgPath, []byte(
		"[daemon]\ninclude_cron = [\""+filepath.Join(filepath.Dir(cfgPath), "crontabs", "*")+"\"]\n"), 0o644))

	p, err := c.Compute(context.Background())
	require.NoError(t, err)

	require.False(t, p.Blocked(), "%v", blockerKinds(p))
	assert.True(t, p.NothingToDo())
}

// TestCompute_CollectsEveryBlockerRatherThanTheFirst matters for --dry-run: an
// operator fixing a box should see the whole list, not discover the next one
// after each attempt.
func TestCompute_CollectsEveryBlockerRatherThanTheFirst(t *testing.T) {
	c, _, _ := fixture{
		euid:        1000,
		username:    "someone",
		scanBlocked: []string{"/etc/cron.d/web: not owned by root"},
		crontabs:    map[string]string{"backup": oneJob},
		cronUnit:    "cron.service",
		cronActive:  true,
	}.build(t)

	p, err := c.Compute(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []BlockerKind{BlockerNeedsRoot, BlockerCronSourcesFailed}, blockerKinds(p))
}
