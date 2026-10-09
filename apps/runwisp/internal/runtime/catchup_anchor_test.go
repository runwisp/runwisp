// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunMissedTickCatchUp_RecordedRowAnchorsNextRestart is the integration
// guard for the "don't re-alert on every restart" contract. The first catch-up
// records a single browsable missed row anchored at the latest missed tick; on
// a second restart inside the same tick window, that row is the new anchor, so
// the daemon detects zero further misses and raises no second alert. Without
// the missed row carrying CreatedAt = latest tick this would re-page on every
// boot.
func TestRunMissedTickCatchUp_RecordedRowAnchorsNextRestart(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	eb := events.NewEventBus()
	jm := NewTaskManager(new(testutil.MockExecutor), eb, time.Now)

	// Wire the manager's persistence to the real in-memory store so the missed
	// row that RecordMissedRun creates is queryable by the second catch-up.
	var persistMu sync.Mutex
	jm.BindPersistenceHook(func(ctx context.Context, run *model.Run, isNew bool) {
		persistMu.Lock()
		defer persistMu.Unlock()
		if isNew {
			require.NoError(t, db.CreateRun(ctx, run.Copy()))
			return
		}
		require.NoError(t, db.UpdateRun(ctx, run.Copy()))
	})

	// catch_up = 0: detection is independent of the re-run policy, so the
	// only DB write the catch-up makes is the single missed row — no executor.
	task := catchupTask(0)
	jm.UpsertTask(task)
	tasks := map[string]*model.Task{task.Name: task}

	// Register the task earlier and seed a prior successful run 20 minutes
	// back, so the registration's last run is a past anchor: at */5 that's
	// four missed ticks by 10:20.
	anchor := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	mustRegister(t, db, task.Name, anchor.Add(-time.Hour))
	require.NoError(t, db.CreateRun(context.Background(), &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    task.Name,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByCron,
		CreatedAt:   anchor,
	}))

	now1 := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
	res1 := runCatchUp(context.Background(), db, tasks, jm, now1, time.UTC)
	assert.Equal(t, 0, res1.Errors)
	assert.Equal(t, 0, res1.Triggered, "skip re-fires nothing")

	// The missed row lands asynchronously; wait for exactly one to be recorded.
	require.Eventually(t, func() bool {
		s, sErr := db.GetRunSummary(context.Background())
		return sErr == nil && s.Missed == 1
	}, time.Second, 5*time.Millisecond, "the first catch-up records exactly one missed row")

	// The missed row must now be the anchor, dated at the latest missed tick.
	reg, err := db.GetTaskRegistration(context.Background(), task.Name)
	require.NoError(t, err)
	require.NotNil(t, reg.LastRunAt)
	assert.True(t, now1.Equal(*reg.LastRunAt),
		"missed row anchors at the latest tick: want %s, got %s", now1, *reg.LastRunAt)

	// Second restart two minutes later — still inside the same */5 window, so
	// there is nothing new to miss.
	now2 := time.Date(2026, 4, 7, 10, 22, 0, 0, time.UTC)
	res2 := runCatchUp(context.Background(), db, tasks, jm, now2, time.UTC)
	assert.Equal(t, 0, res2.Errors)
	assert.Equal(t, 0, res2.Triggered)

	// No second alert: the missed count must stay at one. Give any stray async
	// write a moment to surface so this isn't a false pass.
	require.Never(t, func() bool {
		s, sErr := db.GetRunSummary(context.Background())
		return sErr == nil && s.Missed != 1
	}, 100*time.Millisecond, 10*time.Millisecond,
		"a restart inside the same tick window must not record a second missed row")
}

// TestSnapshotCatchupAnchors_FreezesBeforeContaminatingWrites is the
// regression test for the boot-order bug: production calls
// SnapshotCatchupAnchors before the scheduler starts or run_on_start fires,
// specifically because either can write a fresh run for this boot before
// catch-up ever runs (catch-up itself is deferred until notify subscribes).
// Before the fix, the equivalent of this anchor read happened live, at
// catch-up time — so a run_on_start task's own boot-time run (or a tick the
// live scheduler already fired) became "the last run", and a real downtime
// gap was silently reported as zero missed ticks.
func TestSnapshotCatchupAnchors_FreezesBeforeContaminatingWrites(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	task := catchupTask(100) // */5 * * * *, catch_up: 100
	task.RunOnStart = true
	tasks := map[string]*model.Task{task.Name: task}

	// The real last run before the daemon went down: six hours ago.
	staleAnchor := time.Date(2026, 4, 7, 4, 0, 0, 0, time.UTC)
	mustRegister(t, db, task.Name, staleAnchor.Add(-time.Hour))
	require.NoError(t, db.CreateRun(ctx, &model.Run{
		ID:        ulid.Make().String(),
		TaskName:  task.Name,
		Status:    model.PhaseEnded,
		EndReason: model.EndReasonPtr(model.ReasonSuccess),
		CreatedAt: staleAnchor,
	}))

	now := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)

	// Snapshot the anchor at true boot time, before anything else can write a
	// run for this boot.
	anchors, errs := SnapshotCatchupAnchors(ctx, db, tasks, now)
	require.Equal(t, 0, errs)

	// Simulate what RunStartupTasks (or a live scheduler tick) does next in
	// the real boot sequence: create a fresh run for this boot, stamped "now".
	require.NoError(t, db.CreateRun(ctx, &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    task.Name,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByStartup,
		CreatedAt:   now,
	}))

	// Prove the contamination is real: a live query for "the last run" now
	// returns the boot-time run, not the stale one — this is exactly what the
	// pre-fix code queried at catch-up time.
	reg, err := db.GetTaskRegistration(ctx, task.Name)
	require.NoError(t, err)
	require.True(t, reg.LastRunAt.Equal(now), "sanity: the boot-time run is now the task's last run")

	runner := &fakeTaskRunner{}

	result := RunMissedTickCatchUp(tasks, runner, now, time.UTC, anchors, errs)

	// Six hours at */5 = 72 missed ticks, detected despite the DB's "last run"
	// now being the contaminating boot-time write.
	assert.Equal(t, 72, result.Triggered,
		"catch-up must use the frozen pre-boot anchor, not the live (contaminated) last run")
}

// TestSnapshotCatchupAnchors_SurvivesRunDeletion guards the anchor against
// run history being deleted. keep_runs = 0, a keep_for shorter than the
// schedule's period, storage.max_size, or an operator clearing history can all
// remove a task's latest run. The anchor must still be that run's time, not
// fall back to the task's first-seen time, or the next restart reports every
// tick since first registration as missed and re-runs the task.
func TestSnapshotCatchupAnchors_SurvivesRunDeletion(t *testing.T) {
	ctx := context.Background()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task := catchupTask(1)
	tasks := map[string]*model.Task{task.Name: task}

	firstSeen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, errs := SnapshotCatchupAnchors(ctx, db, tasks, firstSeen)
	require.Zero(t, errs)

	lastRunAt := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	runID := ulid.Make().String()
	require.NoError(t, db.CreateRun(ctx, &model.Run{
		ID:          runID,
		TaskName:    task.Name,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByCron,
		CreatedAt:   lastRunAt,
	}))
	// Retention removes the run.
	require.NoError(t, db.DeleteRunsByIDs(ctx, []string{runID}))

	anchors, errs := SnapshotCatchupAnchors(ctx, db, tasks, lastRunAt.Add(time.Minute))
	require.Zero(t, errs)
	assert.True(t, lastRunAt.Equal(anchors[task.Name]),
		"anchor = %v, want the deleted run's time %v", anchors[task.Name], lastRunAt)
}

// TestSnapshotCatchupAnchors_RemovedTaskStartsFresh is the regression for a
// task that leaves the config and later comes back. Its old registration (and
// with it the last run and any pause) must be forgotten while it is gone, so on
// return it anchors at the boot that re-adds it. Otherwise the whole removed
// period is reported as "missed (daemon was down)", alerted, and re-run.
func TestSnapshotCatchupAnchors_RemovedTaskStartsFresh(t *testing.T) {
	ctx := context.Background()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task := catchupTask(1)
	tasks := map[string]*model.Task{task.Name: task}

	firstSeen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, errs := SnapshotCatchupAnchors(ctx, db, tasks, firstSeen)
	require.Zero(t, errs)
	require.NoError(t, db.CreateRun(ctx, &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    task.Name,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByCron,
		CreatedAt:   firstSeen.Add(time.Hour),
	}))
	require.NoError(t, db.PauseTaskSchedule(ctx, task.Name, firstSeen.Add(2*time.Hour)))

	// Boot with the task commented out of the config.
	_, errs = SnapshotCatchupAnchors(ctx, db, map[string]*model.Task{}, firstSeen.Add(3*time.Hour))
	require.Zero(t, errs)

	// A month later the task is back.
	back := firstSeen.AddDate(0, 1, 0)
	anchors, errs := SnapshotCatchupAnchors(ctx, db, tasks, back)
	require.Zero(t, errs)
	assert.True(t, back.Equal(anchors[task.Name]),
		"anchor = %v, want the boot that re-added the task %v", anchors[task.Name], back)

	reg, err := db.GetTaskRegistration(ctx, task.Name)
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Nil(t, reg.PausedAt, "the old pause must not come back with the task")
	assert.Nil(t, reg.LastRunAt, "the old last run must not come back with the task")

	runs, err := db.CountRunsFiltered(ctx, model.RunFilter{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, runs, "run history is kept")
}
