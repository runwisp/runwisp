// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

	// catch_up = "skip": detection is independent of the re-run policy, so the
	// only DB write the catch-up makes is the single missed row — no executor.
	task := catchupTask(model.MissedRunSkip)
	jm.UpsertTask(task)
	tasks := map[string]*model.Task{task.Name: task}

	// Seed a prior successful run 20 minutes back so GetLastRunByTask has a
	// past anchor: at */5 that's four missed ticks by 10:20.
	anchor := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
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
	last, err := db.GetLastRunByTask(context.Background(), task.Name)
	require.NoError(t, err)
	require.NotNil(t, last.EndReason)
	assert.Equal(t, model.ReasonMissed, *last.EndReason)
	assert.True(t, now1.Equal(last.CreatedAt),
		"missed row anchors at the latest tick: want %s, got %s", now1, last.CreatedAt)

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
	task := catchupTask(model.MissedRunAll) // */5 * * * *, MaxCatchUpRuns: 100
	task.RunOnStart = true
	tasks := map[string]*model.Task{task.Name: task}

	// The real last run before the daemon went down: six hours ago.
	staleAnchor := time.Date(2026, 4, 7, 4, 0, 0, 0, time.UTC)
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
	lastRun, err := db.GetLastRunByTask(ctx, task.Name)
	require.NoError(t, err)
	require.True(t, lastRun.CreatedAt.Equal(now), "sanity: the boot-time run is now the DB's most recent row")

	runner := new(mockTaskRunner)
	runner.On("RecordMissedRun", task.Name, mock.Anything, mock.Anything).Return(nil)
	runner.On("TriggerRun", task.Name, model.TriggeredByCron).Return(&model.Run{}, nil)

	result := RunMissedTickCatchUp(tasks, runner, now, time.UTC, anchors, errs)

	// Six hours at */5 = 72 missed ticks, detected despite the DB's "last run"
	// now being the contaminating boot-time write.
	assert.Equal(t, 72, result.Triggered,
		"catch-up must use the frozen pre-boot anchor, not the live (contaminated) last run")
}
