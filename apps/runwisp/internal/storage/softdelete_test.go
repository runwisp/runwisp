// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTerminalRun(taskName string) *sqlcdb.Run {
	return &sqlcdb.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      sqlcdb.PhaseEnded,
		EndReason:   sqlcdb.EndReasonPtr(sqlcdb.ReasonSuccess),
		TriggeredBy: sqlcdb.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
}

func TestSoftDeleteHidesRunFromReads(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	r1 := newTerminalRun("task1")
	r2 := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(r1))
	require.NoError(t, db.CreateRun(r2))

	refs, err := db.SoftDeleteRuns(model.RunSelector{IDs: []string{r1.ID}}, time.Now())
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, r1.ID, refs[0].ID)
	assert.Equal(t, r1.TaskName, refs[0].TaskName)

	// GetRun: soft-deleted row is hidden.
	_, err = db.GetRun(r1.ID)
	assert.ErrorIs(t, err, ErrNotFound)

	// GetRun: peer row is still visible.
	got, err := db.GetRun(r2.ID)
	require.NoError(t, err)
	assert.Equal(t, r2.ID, got.ID)

	// QueryRuns: only the surviving run shows up.
	runs, err := db.QueryRuns("task1", 10, 0, "", SortColumnDefault, SortDirectionDefault, "")
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, r2.ID, runs[0].ID)

	// Counts ignore soft-deleted rows.
	count, err := db.CountRuns("task1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	countF, err := db.CountRunsFiltered("", "task1", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), countF)
}

func TestSoftDeleteSkipsActiveRuns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	terminal := newTerminalRun("task1")
	running := &sqlcdb.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      sqlcdb.PhaseRunning,
		TriggeredBy: sqlcdb.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	pending := &sqlcdb.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      sqlcdb.PhasePending,
		TriggeredBy: sqlcdb.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(terminal))
	require.NoError(t, db.CreateRun(running))
	require.NoError(t, db.CreateRun(pending))

	refs, err := db.SoftDeleteRuns(
		model.RunSelector{IDs: []string{terminal.ID, running.ID, pending.ID}},
		time.Now(),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1, "only the terminal run should be soft-deleted")
	assert.Equal(t, terminal.ID, refs[0].ID)

	// Active runs remain visible.
	_, err = db.GetRun(running.ID)
	require.NoError(t, err)
	_, err = db.GetRun(pending.ID)
	require.NoError(t, err)
}

func TestRestoreRunsReversesSoftDelete(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	r := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(r))

	_, err := db.SoftDeleteRuns(model.RunSelector{IDs: []string{r.ID}}, time.Now())
	require.NoError(t, err)

	// Sanity: hidden.
	_, err = db.GetRun(r.ID)
	require.ErrorIs(t, err, ErrNotFound)

	restored, err := db.RestoreRuns(model.RunSelector{IDs: []string{r.ID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	assert.Equal(t, r.ID, restored[0].ID)

	got, err := db.GetRun(r.ID)
	require.NoError(t, err)
	assert.Equal(t, r.ID, got.ID)
}

func TestPurgeExpiredSoftDeletesHardDeletesPastTTL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	old := newTerminalRun("task1")
	fresh := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(old))
	require.NoError(t, db.CreateRun(fresh))

	// Old: soft-deleted 10s ago. Fresh: just now.
	_, err := db.SoftDeleteRuns(model.RunSelector{IDs: []string{old.ID}}, time.Now().Add(-10*time.Second))
	require.NoError(t, err)
	_, err = db.SoftDeleteRuns(model.RunSelector{IDs: []string{fresh.ID}}, time.Now())
	require.NoError(t, err)

	// TTL=5s: old is past it, fresh is not.
	refs, err := db.PurgeExpiredSoftDeletes(5 * time.Second)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, old.ID, refs[0].ID)

	// Restoring the fresh run should still work — it was not purged.
	restored, err := db.RestoreRuns(model.RunSelector{IDs: []string{fresh.ID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)

	// Restoring the old run finds nothing.
	restored, err = db.RestoreRuns(model.RunSelector{IDs: []string{old.ID}})
	require.NoError(t, err)
	assert.Empty(t, restored)
}

func TestPurgeAllOnBoot(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	r := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(r))
	_, err := db.SoftDeleteRuns(model.RunSelector{IDs: []string{r.ID}}, time.Now())
	require.NoError(t, err)

	// TTL=0 drains everything currently soft-deleted (boot-time scenario).
	refs, err := db.PurgeExpiredSoftDeletes(0)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, r.ID, refs[0].ID)
}

func TestResolveSelectorIDsHonorsStatusFilter(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	terminal := newTerminalRun("task1")
	running := &sqlcdb.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      sqlcdb.PhaseRunning,
		TriggeredBy: sqlcdb.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	other := newTerminalRun("task2")
	require.NoError(t, db.CreateRun(terminal))
	require.NoError(t, db.CreateRun(running))
	require.NoError(t, db.CreateRun(other))

	// MatchAll for task1 without status filter resolves both.
	refs, err := db.ResolveSelectorIDs(
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		"",
	)
	require.NoError(t, err)
	assert.Len(t, refs, 2)

	// Status filter narrows to running rows only.
	refs, err = db.ResolveSelectorIDs(
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		string(sqlcdb.PhaseRunning),
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)

	// Soft-deleted rows are excluded.
	_, err = db.SoftDeleteRuns(model.RunSelector{IDs: []string{terminal.ID}}, time.Now())
	require.NoError(t, err)
	refs, err = db.ResolveSelectorIDs(
		model.RunSelector{MatchAll: true, Filter: model.RunFilter{TaskName: "task1"}},
		"",
	)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, running.ID, refs[0].ID)
}

func TestSelectorMatchAllRespectsExceptIDs(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	keep := newTerminalRun("task1")
	skip := newTerminalRun("task1")
	require.NoError(t, db.CreateRun(keep))
	require.NoError(t, db.CreateRun(skip))

	refs, err := db.SoftDeleteRuns(
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
	_, err = db.GetRun(skip.ID)
	require.NoError(t, err)
}
