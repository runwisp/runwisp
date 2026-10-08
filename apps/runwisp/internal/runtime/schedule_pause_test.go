// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pausableTask(name string) *model.Task {
	return &model.Task{Name: name, Cron: "0 3 * * *", Run: "echo hi", ManualTrigger: true}
}

func newPauseDB(t *testing.T) *storage.SQLiteDatabase {
	t.Helper()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestSchedulerPause_SkipsTicksUntilResume(t *testing.T) {
	runner := &fakeTaskRunner{}
	sched := NewScheduler(runner, map[string]*model.Task{"nightly": pausableTask("nightly")}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	require.NoError(t, sched.Pause(context.Background(), "nightly"))
	sched.fireOnce("nightly", time.UTC, false)
	assert.Equal(t, 0, runner.triggerCount(), "a paused schedule must not fire")
	assert.Empty(t, runner.skips, "a paused tick leaves no run row")
	assert.Nil(t, sched.GetNextRun("nightly"), "a paused task has no next run")
	assert.NotNil(t, sched.PausedAt("nightly"))

	require.NoError(t, sched.Resume(context.Background(), "nightly"))
	sched.fireOnce("nightly", time.UTC, false)
	assert.Equal(t, 1, runner.triggerCount(), "a resumed schedule fires again")
	assert.NotNil(t, sched.GetNextRun("nightly"))
	assert.Nil(t, sched.PausedAt("nightly"))
}

func TestSchedulerPause_IdempotentKeepsFirstPauseTime(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC))
	sched := NewScheduler(&fakeTaskRunner{}, map[string]*model.Task{"nightly": pausableTask("nightly")}, time.UTC, clk.Now)

	require.NoError(t, sched.Pause(context.Background(), "nightly"))
	first := *sched.PausedAt("nightly")
	clk.Advance(time.Hour)
	require.NoError(t, sched.Pause(context.Background(), "nightly"))
	assert.Equal(t, first, *sched.PausedAt("nightly"), "re-pausing keeps the original pause time")

	require.NoError(t, sched.Resume(context.Background(), "nightly"))
	require.NoError(t, sched.Resume(context.Background(), "nightly"), "resuming an unpaused task is a no-op")
}

func TestSchedulerPause_RejectsUnpausable(t *testing.T) {
	service := pausableTask("svc")
	service.Kind = model.KindService
	manual := pausableTask("manual")
	manual.Cron = ""
	locked := pausableTask("locked")
	locked.ManualTrigger = false
	held := pausableTask("held")
	held.HeldBy = model.HeldByCron

	tasks := map[string]*model.Task{"svc": service, "manual": manual, "locked": locked, "held": held}
	sched := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, nil)

	for _, name := range []string{"svc", "manual", "locked", "held", "missing"} {
		err := sched.Pause(context.Background(), name)
		assert.ErrorIs(t, err, ErrNotPausable, name)
		assert.Nil(t, sched.PausedAt(name), name)
	}
}

// Reload reschedules a changed task via RemoveTask+AddTask and re-plans jitter
// for the whole set; neither may silently lift an operator's pause.
func TestSchedulerPause_SurvivesRescheduleAndJitterRecompute(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := pausableTask("nightly")
	tasks := map[string]*model.Task{"nightly": task}
	sched := NewScheduler(runner, tasks, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	require.NoError(t, sched.Pause(context.Background(), "nightly"))

	changed := pausableTask("nightly")
	changed.Cron = "0 4 * * *"
	sched.RemoveTask("nightly")
	require.NoError(t, sched.AddTask(changed))
	sched.RecomputeJitter(map[string]*model.Task{"nightly": changed})

	sched.fireOnce("nightly", time.UTC, false)
	assert.Equal(t, 0, runner.triggerCount())
	assert.NotNil(t, sched.PausedAt("nightly"), "a cron change keeps the pause")
	assert.Nil(t, sched.GetNextRun("nightly"))
}

func TestSchedulerPause_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	db := newPauseDB(t)
	clk := testutil.NewClock(time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC))
	tasks := map[string]*model.Task{"nightly": pausableTask("nightly")}

	first := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, clk.Now)
	_, err := first.RestorePauses(ctx, db)
	require.NoError(t, err)
	require.NoError(t, first.Pause(ctx, "nightly"))

	// A fresh scheduler over the same store is the daemon after a restart.
	second := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, clk.Now)
	cleared, err := second.RestorePauses(ctx, db)
	require.NoError(t, err)
	assert.Empty(t, cleared)
	require.NotNil(t, second.PausedAt("nightly"))
	assert.True(t, second.PausedAt("nightly").Equal(clk.Now()))

	clk.Advance(time.Hour)
	require.NoError(t, second.Resume(ctx, "nightly"))
	reg, err := db.GetTaskRegistration(ctx, "nightly")
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Nil(t, reg.PausedAt)
	require.NotNil(t, reg.ResumedAt)
	assert.True(t, reg.ResumedAt.Equal(clk.Now()))
}

// TOML wins: a reload (or a boot) with a config that no longer lets the task be
// paused clears the pause, in memory and on disk, and says so. A cron change
// alone keeps it.
func TestSchedulerPrunePauses_ClearsWhatConfigForbids(t *testing.T) {
	ctx := context.Background()
	db := newPauseDB(t)
	names := []string{"locked", "cronless", "removed", "service", "rescheduled"}
	tasks := make(map[string]*model.Task, len(names))
	for _, name := range names {
		tasks[name] = pausableTask(name)
	}
	sched := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, nil)
	_, err := sched.RestorePauses(ctx, db)
	require.NoError(t, err)
	for _, name := range names {
		require.NoError(t, sched.Pause(ctx, name))
	}

	next := map[string]*model.Task{
		"locked":      pausableTask("locked"),
		"cronless":    pausableTask("cronless"),
		"service":     pausableTask("service"),
		"rescheduled": pausableTask("rescheduled"),
	}
	next["locked"].ManualTrigger = false
	next["cronless"].Cron = ""
	next["service"].Kind = model.KindService
	next["rescheduled"].Cron = "0 5 * * *"

	cleared := sched.PrunePauses(next)
	assert.Equal(t, []string{
		`task "cronless": schedule pause cleared (it has no cron schedule)`,
		`task "locked": schedule pause cleared (manual_trigger = false)`,
		`task "removed": schedule pause cleared (not in runwisp.toml)`,
		`task "service": schedule pause cleared (it is a service)`,
	}, cleared)
	assert.NotNil(t, sched.PausedAt("rescheduled"))

	paused, err := db.ListPausedTaskSchedules(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"rescheduled"}, keysOf(paused), "cleared pauses are cleared on disk too")
}

// A pause left in the store for a task the config no longer allows (edited
// while the daemon was down) is cleared at boot rather than resurrected.
func TestSchedulerRestorePauses_ClearsStaleRows(t *testing.T) {
	ctx := context.Background()
	db := newPauseDB(t)
	at := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	require.NoError(t, db.PauseTaskSchedule(ctx, "gone", at))
	require.NoError(t, db.PauseTaskSchedule(ctx, "nightly", at))

	sched := NewScheduler(&fakeTaskRunner{}, map[string]*model.Task{"nightly": pausableTask("nightly")}, time.UTC, nil)
	cleared, err := sched.RestorePauses(ctx, db)
	require.NoError(t, err)
	assert.Equal(t, []string{`task "gone": schedule pause cleared (not in runwisp.toml)`}, cleared)
	assert.NotNil(t, sched.PausedAt("nightly"))
	assert.Nil(t, sched.PausedAt("gone"))
}

// A jittered fire can already be waiting in the gate when the operator pauses
// the task; it must not start once the gate frees up.
func TestScheduleJitteredRun_PausedTaskFireRefused(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	exec := testutil.NewGateExecutor()
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, clk.Now).(*defaultTaskManager)
	t.Cleanup(jm.Shutdown)

	a := testTask("a", model.PolicySkip, 1)
	b := testTask("b", model.PolicySkip, 1)
	b.Cron, b.ManualTrigger = "0 3 * * *", true
	jm.UpsertTask(a)
	jm.UpsertTask(b)
	sched := NewScheduler(jm, map[string]*model.Task{"a": a, "b": b}, time.UTC, clk.Now)

	created := watchRuns(eb, events.EventRunCreated)

	tick := clk.Now()
	jm.ScheduleJitteredRun("a", tick, tick, time.Hour)
	exec.WaitStarted(t)
	jm.ScheduleJitteredRun("b", tick, tick.Add(time.Hour), time.Hour)

	require.NoError(t, sched.Pause(context.Background(), "b"))
	baseline := created.count()
	exec.ReleaseAll()

	require.Never(t, func() bool {
		for _, r := range created.snapshot()[baseline:] {
			if r.TaskName == "b" {
				return true
			}
		}
		return false
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a paused task's already-queued jitter fire must not start a run")
	assert.Equal(t, 1, exec.Calls(), "only 'a' should ever have executed")
}

// A paused task owes no catch-up, and once resumed the anchor is at least the
// resume time, so a later restart never counts the paused window as missed.
func TestSnapshotCatchupAnchors_PauseWindowIsNotMissed(t *testing.T) {
	ctx := context.Background()
	db := newPauseDB(t)
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	task := catchupTask(0)
	task.ManualTrigger = true
	tasks := map[string]*model.Task{task.Name: task}

	require.NoError(t, db.EnsureTaskRegistered(ctx, task.Name, now.Add(-2*time.Hour)))
	require.NoError(t, db.PauseTaskSchedule(ctx, task.Name, now.Add(-90*time.Minute)))

	anchors, errs := SnapshotCatchupAnchors(ctx, db, tasks, now)
	assert.Equal(t, 0, errs)
	assert.NotContains(t, anchors, task.Name, "a paused task is not caught up")

	resumedAt := now.Add(-10 * time.Minute)
	require.NoError(t, db.ResumeTaskSchedule(ctx, task.Name, resumedAt))
	anchors, errs = SnapshotCatchupAnchors(ctx, db, tasks, now)
	assert.Equal(t, 0, errs)
	require.Contains(t, anchors, task.Name)
	assert.True(t, anchors[task.Name].Equal(resumedAt), "the anchor moves up to the resume time")
}

func keysOf[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
