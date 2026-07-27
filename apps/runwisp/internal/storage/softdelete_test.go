// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTerminalRun(taskName string) *model.Run {
	return &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
}

func TestSoftDeleteHidesRunFromReads(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	r1 := newTerminalRun("task1")
	r2 := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, r1))
	require.NoError(t, db.CreateRun(ctx, r2))

	refs, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{r1.ID}}, time.Now())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, r1.ID, refs[0].ID)
	assert.Equal(t, r1.TaskName, refs[0].TaskName)

	// GetRun: soft-deleted row is hidden.
	_, err = db.GetRun(ctx, r1.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// GetRun: peer row is still visible.
	got, err := db.GetRun(ctx, r2.ID)
	require.NoError(t, err)
	assert.Equal(t, r2.ID, got.ID)

	// QueryRuns: only the surviving run shows up.
	runs, err := db.QueryRuns(ctx, RunQuery{Filter: model.RunFilter{TaskName: "task1"}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, r2.ID, runs[0].ID)

	// Counts ignore soft-deleted rows.
	count, err := db.CountRuns(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	countF, err := db.CountRunsFiltered(ctx, model.RunFilter{TaskName: "task1"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), countF)
}

func TestSoftDeleteSkipsActiveRuns(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	terminal := newTerminalRun("task1")
	running := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      model.PhaseRunning,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	pending := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(ctx, terminal))
	require.NoError(t, db.CreateRun(ctx, running))
	require.NoError(t, db.CreateRun(ctx, pending))

	refs, err := db.SoftDeleteRuns(ctx,
		model.RunSelector{IDs: []string{terminal.ID, running.ID, pending.ID}},
		time.Now(),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1, "only the terminal run should be soft-deleted")
	assert.Equal(t, terminal.ID, refs[0].ID)

	// Active runs remain visible.
	_, err = db.GetRun(ctx, running.ID)
	require.NoError(t, err)
	_, err = db.GetRun(ctx, pending.ID)
	require.NoError(t, err)
}

func TestRestoreRunsReversesSoftDelete(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	r := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, r))

	_, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{r.ID}}, time.Now())
	require.NoError(t, err)

	// Sanity: hidden.
	_, err = db.GetRun(ctx, r.ID)
	require.ErrorIs(t, err, ErrNotFound)

	restored, err := db.RestoreRuns(ctx, model.RunSelector{IDs: []string{r.ID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	assert.Equal(t, r.ID, restored[0].ID)

	got, err := db.GetRun(ctx, r.ID)
	require.NoError(t, err)
	assert.Equal(t, r.ID, got.ID)
}

func TestPurgeExpiredSoftDeletesHardDeletesPastTTL(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	old := newTerminalRun("task1")
	fresh := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, old))
	require.NoError(t, db.CreateRun(ctx, fresh))

	// Old: soft-deleted 10s ago. Fresh: just now.
	_, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{old.ID}}, time.Now().Add(-10*time.Second))
	require.NoError(t, err)
	_, err = db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{fresh.ID}}, time.Now())
	require.NoError(t, err)

	// TTL=5s: old is past it, fresh is not.
	refs, err := db.PurgeExpiredSoftDeletes(ctx, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, old.ID, refs[0].ID)

	// Restoring the fresh run should still work — it was not purged.
	restored, err := db.RestoreRuns(ctx, model.RunSelector{IDs: []string{fresh.ID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)

	// Restoring the old run finds nothing.
	restored, err = db.RestoreRuns(ctx, model.RunSelector{IDs: []string{old.ID}})
	require.NoError(t, err)
	assert.Empty(t, restored)
}

func TestRestoreRunsMatchAllReversesSoftDelete(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	r1 := newTerminalRun("task1")
	r2 := newTerminalRun("task1")
	r3 := newTerminalRun("task2")
	for _, r := range []*model.Run{r1, r2, r3} {
		require.NoError(t, db.CreateRun(ctx, r))
	}

	// Soft-delete all three runs.
	_, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{r1.ID, r2.ID, r3.ID}}, time.Now())
	require.NoError(t, err)

	// MatchAll for task1 restores both r1 and r2 (task2's r3 is untouched).
	restored, err := db.RestoreRuns(ctx, model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}})
	require.NoError(t, err)
	require.Len(t, restored, 2)

	// r3 is still soft-deleted.
	_, err = db.GetRun(ctx, r3.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestRestoreRunsMatchAllReturnsOnlyRestoredRows guards the regression where
// RestoreRuns (MatchAll) re-selected every non-deleted run matching the filter
// instead of only the rows it restored — inflating the affected count and
// triggering a run.updated event storm for runs that were never soft-deleted.
func TestRestoreRunsMatchAllReturnsOnlyRestoredRows(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	// One soft-deleted run and several live runs, all matching the filter.
	deleted := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, deleted))
	var live []*model.Run
	for i := 0; i < 5; i++ {
		r := newTerminalRun("task1")
		require.NoError(t, db.CreateRun(ctx, r))
		live = append(live, r)
	}

	_, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{deleted.ID}}, time.Now())
	require.NoError(t, err)

	restored, err := db.RestoreRuns(ctx, model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}})
	require.NoError(t, err)
	require.Len(t, restored, 1, "restore must return only the row it actually restored")
	assert.Equal(t, deleted.ID, restored[0].ID)

	// Sanity: the live rows were never touched and are still visible.
	for _, r := range live {
		_, err := db.GetRun(ctx, r.ID)
		require.NoError(t, err)
	}
}

func TestPurgeAllOnBoot(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	r := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, r))
	_, err := db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{r.ID}}, time.Now())
	require.NoError(t, err)

	// TTL=0 drains everything currently soft-deleted (boot-time scenario).
	refs, err := db.PurgeExpiredSoftDeletes(ctx, 0)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, r.ID, refs[0].ID)
}

func TestResolveSelectorIDsHonorsStatusFilter(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	terminal := newTerminalRun("task1")
	running := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      model.PhaseRunning,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	other := newTerminalRun("task2")
	require.NoError(t, db.CreateRun(ctx, terminal))
	require.NoError(t, db.CreateRun(ctx, running))
	require.NoError(t, db.CreateRun(ctx, other))

	// MatchAll for task1 without status filter resolves both.
	refs, err := db.ResolveSelectorIDs(ctx,
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		"",
	)
	require.NoError(t, err)
	assert.Len(t, refs, 2)

	// Status filter narrows to running rows only.
	refs, err = db.ResolveSelectorIDs(ctx,
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		string(model.PhaseRunning),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)

	// Soft-deleted rows are excluded.
	_, err = db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{terminal.ID}}, time.Now())
	require.NoError(t, err)
	refs, err = db.ResolveSelectorIDs(ctx,
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		"",
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)
}

func TestResolveSelectorIDsByExplicitIDs(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	terminal := newTerminalRun("task1")
	running := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      model.PhaseRunning,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(ctx, terminal))
	require.NoError(t, db.CreateRun(ctx, running))

	// Explicit IDs path without a status filter resolves both rows.
	refs, err := db.ResolveSelectorIDs(ctx,
		model.RunSelector{IDs: []string{terminal.ID, running.ID}},
		"",
	)
	require.NoError(t, err)
	assert.Len(t, refs, 2)

	// Status filter narrows the explicit-ID set.
	refs, err = db.ResolveSelectorIDs(ctx,
		model.RunSelector{IDs: []string{terminal.ID, running.ID}},
		string(model.PhaseRunning),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)

	// Soft-deleted rows drop out of the IDs path too.
	_, err = db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{terminal.ID}}, time.Now())
	require.NoError(t, err)
	refs, err = db.ResolveSelectorIDs(ctx,
		model.RunSelector{IDs: []string{terminal.ID, running.ID}},
		"",
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)
}

func TestSelectorMatchAllRespectsExceptIDs(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	keep := newTerminalRun("task1")
	skip := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(ctx, keep))
	require.NoError(t, db.CreateRun(ctx, skip))

	refs, err := db.SoftDeleteRuns(ctx,
		model.RunSelector{
			MatchAll:  true,
			Filter:    model.RunFilter{TaskName: "task1"},
			ExceptIDs: []string{skip.ID},
		},
		time.Now(),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, keep.ID, refs[0].ID)

	// skip is still visible.
	_, err = db.GetRun(ctx, skip.ID)
	require.NoError(t, err)
}

// TestSelectorMatchAllNewGateWithExceptIDs proves the new named filter gates
// (here triggered_by) compose with the trailing `except_ids` slice: sqlc
// appends the slice's bind params after every named scalar, so a new gate must
// not disturb that ordering. A regression here surfaces as a SQL bind mismatch.
func TestSelectorMatchAllNewGateWithExceptIDs(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	apiKeep := newTerminalRun("task1")
	apiSkip := newTerminalRun("task1")
	cronRun := newTerminalRun("task1")
	cronRun.TriggeredBy = model.TriggeredByCron
	for _, r := range []*model.Run{apiKeep, apiSkip, cronRun} {
		require.NoError(t, db.CreateRun(ctx, r))
	}

	// MatchAll api-triggered runs, excepting apiSkip — cronRun is filtered out
	// by the gate, apiSkip by the slice, leaving only apiKeep.
	refs, err := db.SoftDeleteRuns(ctx,
		model.RunSelector{
			MatchAll:  true,
			Filter:    model.RunFilter{TriggeredBy: string(model.TriggeredByAPI)},
			ExceptIDs: []string{apiSkip.ID},
		},
		time.Now(),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, apiKeep.ID, refs[0].ID)

	// The excepted run and the gate-filtered run both survive.
	_, err = db.GetRun(ctx, apiSkip.ID)
	require.NoError(t, err)
	_, err = db.GetRun(ctx, cronRun.ID)
	require.NoError(t, err)
}
