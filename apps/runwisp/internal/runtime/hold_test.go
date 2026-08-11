// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests pin the hold gate: a task whose crontab a live system cron daemon
// is still reading must not fire from RunWisp as well. Both schedulers running
// the same job is invisible — nothing errors, the job simply runs twice — so the
// guarantee has to be structural rather than a warning the operator might miss.

// holdCatchupDB is a stateful stand-in for the catch-up half of the run
// repository. Testify's MockRunRepository answers per-call expectations, which
// can't express the one behaviour these tests turn on: EnsureTaskRegistered is
// INSERT OR IGNORE, so the *first* stamp is the anchor and later ones are no-ops.
// Asserting on a real anchor is the only way to prove the hold window is not
// retroactively charged to RunWisp.
type holdCatchupDB struct {
	*testutil.MockRunRepository
	registered map[string]time.Time
}

func newHoldCatchupDB() *holdCatchupDB {
	return &holdCatchupDB{
		MockRunRepository: new(testutil.MockRunRepository),
		registered:        map[string]time.Time{},
	}
}

func (d *holdCatchupDB) EnsureTaskRegistered(_ context.Context, name string, firstSeen time.Time) error {
	if _, exists := d.registered[name]; !exists {
		d.registered[name] = firstSeen
	}
	return nil
}

func (d *holdCatchupDB) GetLastRunByTask(context.Context, string) (*model.Run, error) {
	return nil, nil
}

func (d *holdCatchupDB) GetTaskRegistration(_ context.Context, name string) (*model.TaskRegistration, error) {
	firstSeen, ok := d.registered[name]
	if !ok {
		return nil, nil
	}
	return &model.TaskRegistration{TaskName: name, FirstSeenAt: firstSeen}, nil
}

func heldCronTask(name, cron string) *model.Task {
	return &model.Task{
		Name:       name,
		Cron:       cron,
		Run:        "echo hi",
		Source:     model.SourceCron,
		SourceFile: "/etc/cron.d/" + name,
		HeldBy:     model.HeldByCron,
	}
}

// TestSchedulerDoesNotRegisterHeldTasks is the gate itself. GetNextRun returning
// nil is the observable proof there is no cron entry: a held task can't fire
// because there is nothing registered to fire it.
func TestSchedulerDoesNotRegisterHeldTasks(t *testing.T) {
	runner := &fakeTaskRunner{}
	held := heldCronTask("held", "@every 1s")
	ours := &model.Task{Name: "ours", Cron: "@every 1s", Run: "echo hi"}
	tasks := map[string]*model.Task{"held": held, "ours": ours}

	sched := NewScheduler(runner, tasks, time.UTC, nil)
	result, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	assert.Equal(t, 1, result.Scheduled, "only the task RunWisp owns is scheduled")
	assert.Equal(t, 1, result.Held, "and the held one is counted, not silently dropped")
	assert.Empty(t, result.Warnings)

	assert.Nil(t, sched.GetNextRun("held"), "a held task has no cron entry to fire it")
	assert.NotNil(t, sched.GetNextRun("ours"))
}

// TestSchedulerCountsHeldRunOnStartTask covers the @reboot shape: a crontab's
// `@reboot` line imports as run_on_start with no cron at all, so counting held
// tasks off the cron expression would report zero on a box where every job is
// held — and the operator would see no reason for the silence.
func TestSchedulerCountsHeldRunOnStartTask(t *testing.T) {
	runner := &fakeTaskRunner{}
	held := heldCronTask("boot-job", "")
	held.RunOnStart = true

	sched := NewScheduler(runner, map[string]*model.Task{"boot-job": held}, time.UTC, nil)
	result, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	assert.Equal(t, 0, result.Scheduled)
	assert.Equal(t, 1, result.Held)
}

// TestAddTaskRefusesHeldTask covers the reload path into the running scheduler.
func TestAddTaskRefusesHeldTask(t *testing.T) {
	runner := &fakeTaskRunner{}
	sched := NewScheduler(runner, map[string]*model.Task{}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	held := heldCronTask("held", "@every 1s")
	require.NoError(t, sched.AddTask(held), "a held task is skipped, not an error")
	assert.Nil(t, sched.GetNextRun("held"))

	// Rescheduling (RemoveTask+AddTask, as the reconciler does) must refuse
	// the same held task.
	sched.RemoveTask(held.Name)
	require.NoError(t, sched.AddTask(held))
	assert.Nil(t, sched.GetNextRun("held"))
}

// TestHeldTaskGetsNoJitterPlan keeps a held task out of the jitter dial. Leaving
// it in would let it take a slot and push the tasks RunWisp actually fires around
// it, so a hold would change when unheld jobs run.
func TestHeldTaskGetsNoJitterPlan(t *testing.T) {
	runner := &fakeTaskRunner{}
	held := heldCronTask("held", "0 3 * * *")
	held.Jitter = 30 * time.Minute
	ours := &model.Task{Name: "ours", Cron: "0 3 * * *", Run: "echo hi", Jitter: 30 * time.Minute}

	now := time.Date(2026, 7, 30, 1, 0, 0, 0, time.UTC)
	sched := NewScheduler(runner, map[string]*model.Task{"held": held, "ours": ours}, time.UTC,
		func() time.Time { return now })
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	_, heldPlanned := sched.jitterPlans["held"]
	assert.False(t, heldPlanned, "a task that cannot fire must not occupy a jitter slot")
	_, oursPlanned := sched.jitterPlans["ours"]
	assert.True(t, oursPlanned)
}

// TestRunStartupTasksSkipsHeldTask is the @reboot double-fire: cron runs a
// @reboot line when cron starts, and RunWisp would run the imported
// run_on_start task when the daemon starts. Both, on the same reboot.
func TestRunStartupTasksSkipsHeldTask(t *testing.T) {
	runner := &fakeTaskRunner{}
	held := heldCronTask("held-boot", "")
	held.RunOnStart = true
	ours := &model.Task{Name: "our-boot", Run: "echo hi", RunOnStart: true}

	result := RunStartupTasks(map[string]*model.Task{
		"held-boot": held, "our-boot": ours,
	}, runner)

	assert.Equal(t, 1, result.Triggered, "only the task RunWisp owns fires at boot")
	assert.Equal(t, 0, result.Errors)
	assert.Equal(t, 1, runner.triggerCount())
}

// TestHeldTaskIsStillManuallyTriggerable pins the deliberate limit of the gate.
// A hold withholds automatic firing only — being able to run a job by hand is how
// an operator checks it works under RunWisp before handing the schedule over, and
// removing that would make the migration a leap of faith.
func TestHeldTaskIsStillManuallyTriggerable(t *testing.T) {
	held := heldCronTask("held", "@every 1s")
	require.False(t, held.Schedulable())

	runner := &fakeTaskRunner{}
	_, err := runner.TriggerRunWithOptions(held.Name, TriggerRunOptions{
		TriggeredBy: model.TriggeredByAPI,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, runner.triggerCount(), "the run manager has no hold concept, by design")
}

// applyHoldDiff runs one reconcile apply over the two task sets with a real
// scheduler and a stateful catch-up DB, so a hold flip can be observed where it
// actually matters: whether a cron entry exists afterwards.
func applyHoldDiff(t *testing.T, now time.Time, old, updated map[string]*model.Task) (*Scheduler, *holdCatchupDB) {
	t.Helper()
	sched := NewScheduler(&fakeTaskRunner{}, old, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	t.Cleanup(sched.Stop)

	db := newHoldCatchupDB()
	r := &Reconciler{
		registry:  NewTaskRegistry(old),
		manager:   &recordingManager{},
		scheduler: sched,
		db:        db,
		now:       func() time.Time { return now },
	}
	r.apply(config.DiffTasks(old, updated), old, updated)
	return sched, db
}

// TestReconcileRegistersTaskWhenHoldLifts is the whole point of leaving HeldBy
// out of sameDefinition's mask. Retiring cron and reloading changes only that one
// derived field; if the reload treated it as provenance the task would be
// Restamped — registry pointer swapped, scheduler never told — and a job the
// operator just took ownership of would silently never run.
func TestReconcileRegistersTaskWhenHoldLifts(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 40, 0, 0, time.UTC)
	held := heldCronTask("backup", "0 3 * * *")
	freed := *held
	freed.HeldBy = model.HeldByNothing

	sched, db := applyHoldDiff(t, now, taskSet(held), taskSet(&freed))

	assert.NotNil(t, sched.GetNextRun("backup"),
		"the cron entry has to exist now, or the handover silently dropped the job")
	assert.Equal(t, now, db.registered["backup"],
		"and the catch-up anchor starts at the handover, not at some later restart")
}

// TestReconcileDropsEntryWhenHoldLands is the reverse flip: something brought
// cron back (an operator, a package upgrade) and the next reload has to stand
// RunWisp down again rather than leave both schedulers firing.
func TestReconcileDropsEntryWhenHoldLands(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 40, 0, 0, time.UTC)
	free := &model.Task{Name: "backup", Cron: "0 3 * * *", Run: "backup.sh",
		Source: model.SourceCron, SourceFile: "/etc/cron.d/backup"}
	held := *free
	held.HeldBy = model.HeldByCron

	sched, _ := applyHoldDiff(t, now, taskSet(free), taskSet(&held))

	assert.Nil(t, sched.GetNextRun("backup"), "RunWisp must stop firing what cron took back")
}

// TestReconcileAddedHeldTaskIsNotAnchored covers the reload that *introduces* a
// held task (a new crontab appears under include_cron). Anchoring it now would
// charge the whole hold window to RunWisp as missed ticks the moment cron retires.
func TestReconcileAddedHeldTaskIsNotAnchored(t *testing.T) {
	now := time.Date(2026, 7, 30, 18, 40, 0, 0, time.UTC)
	held := heldCronTask("newcomer", "0 3 * * *")

	sched, db := applyHoldDiff(t, now, taskSet(), taskSet(held))

	assert.Nil(t, sched.GetNextRun("newcomer"))
	assert.NotContains(t, db.registered, "newcomer")
}

// TestCatchUpSkipsHeldTaskAndLeavesNoAnchor is the alert-storm guard. Catch-up
// detection ignores the re-run policy, so even catch_up = "skip" records a missed
// run and alerts on it. If a held task were anchored while cron was running it
// perfectly, retiring cron would page the operator once for every tick cron had
// already handled.
func TestCatchUpSkipsHeldTaskAndLeavesNoAnchor(t *testing.T) {
	db := newHoldCatchupDB()
	runner := &fakeTaskRunner{}
	held := heldCronTask("held", "0 * * * *")
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	result := RunMissedTickCatchUp(context.Background(), db,
		map[string]*model.Task{"held": held}, runner, now, time.UTC)

	assert.Equal(t, 0, result.Triggered)
	assert.Equal(t, 0, result.Errors)
	assert.Equal(t, 0, runner.triggerCount())
	assert.NotContains(t, db.registered, "held",
		"no first-seen anchor while held: the anchor is what the downtime gap is measured from")
}

// TestCatchUpAfterHoldLiftsCountsNoMissedTicks is the end-to-end shape of the
// same guarantee: the first catch-up pass once the job is RunWisp's stamps the
// anchor at that moment, so the hold window is not retroactively reported as
// RunWisp's missed ticks.
func TestCatchUpAfterHoldLiftsCountsNoMissedTicks(t *testing.T) {
	db := newHoldCatchupDB()
	runner := &fakeTaskRunner{}
	task := heldCronTask("job", "0 * * * *")
	held := map[string]*model.Task{"job": task}

	// A week of the daemon running with the job held by cron.
	start := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	RunMissedTickCatchUp(context.Background(), db, held, runner, start, time.UTC)

	// cron is retired, the config reloads, the hold is gone.
	unheld := *task
	unheld.HeldBy = model.HeldByNothing
	later := start.Add(7 * 24 * time.Hour)
	result := RunMissedTickCatchUp(context.Background(), db,
		map[string]*model.Task{"job": &unheld}, runner, later, time.UTC)

	assert.Equal(t, 0, result.Triggered,
		"a week of hourly ticks cron ran is not a week of RunWisp missing them")
	assert.Equal(t, 0, result.Errors)
	assert.Equal(t, later, db.registered["job"], "and the anchor starts where RunWisp took over")
}
