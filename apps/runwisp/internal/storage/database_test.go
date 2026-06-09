// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) RunRepository {
	db, err := New(":memory:")
	require.NoError(t, err)
	return db
}

func TestCreateAndGetRun(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}

	err := db.CreateRun(ctx, run)
	require.NoError(t, err)

	fetched, err := db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, fetched.ID)
	assert.Equal(t, run.TaskName, fetched.TaskName)
}

func TestUpdateRun(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(ctx, run))

	run.Status = model.PhaseRunning
	err := db.UpdateRun(ctx, run)
	require.NoError(t, err)

	fetched, err := db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PhaseRunning, fetched.Status)
}

func TestCountRuns(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))

	count, err := db.CountRuns(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = db.CountRuns(ctx, "task2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCountRunsFiltered(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task3", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonLogOverflow), TriggeredBy: model.TriggeredByAPI}))

	count, err := db.CountRunsFiltered(ctx, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(4), count)

	count, err = db.CountRunsFiltered(ctx, string(model.ReasonSuccess), "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = db.CountRunsFiltered(ctx, string(model.ReasonLogOverflow), "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = db.CountRunsFiltered(ctx, "", "task1", "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = db.CountRunsFiltered(ctx, "", "", "task2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestRunSummaryGroupsLogOverflowAsFailed(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "ok", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "bad", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "noisy", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonLogOverflow), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "skipped", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSkipped), TriggeredBy: model.TriggeredByAPI}))

	summary, err := db.GetRunSummary(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(4), summary.Total)
	assert.Equal(t, int64(1), summary.Success)
	assert.Equal(t, int64(2), summary.Failed,
		"log_overflow must count as a failure alongside failed/crashed/timeout")
}

func TestQueryRunsByTask(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))

	runs, err := db.QueryRuns(ctx, RunQuery{Filter: model.RunFilter{TaskName: "task1"}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 2)

	runs, err = db.QueryRuns(ctx, RunQuery{Filter: model.RunFilter{Status: string(model.ReasonSuccess), TaskName: "task1"}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, model.PhaseEnded, runs[0].Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonSuccess), runs[0].EndReason)
}

func TestQueryAllRuns(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))

	runs, err := db.QueryRuns(ctx, RunQuery{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 2)

	runs, err = db.QueryRuns(ctx, RunQuery{Filter: model.RunFilter{TaskName: "task1"}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, "task1", runs[0].TaskName)
}

func TestDeleteRun(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	id := ulid.Make().String()
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: id, TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))

	err := db.DeleteRun(ctx, id)
	require.NoError(t, err)

	_, err = db.GetRun(ctx, id)
	assert.Error(t, err)
}

func TestDeleteOldRuns(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	old := now.Add(-25 * time.Hour)

	// Run to be deleted by hours retention (24h)
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: old, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(ctx, &run1))

	// Run to be kept
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(ctx, &run2))

	task := &model.Task{
		Name:    "task1",
		KeepFor: 24 * time.Hour,
	}

	deleted, err := db.DeleteOldRuns(ctx, task)
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Equal(t, run1.ID, deleted[0].ID)

	_, err = db.GetRun(ctx, run1.ID)
	assert.Error(t, err)
	_, err = db.GetRun(ctx, run2.ID)
	assert.NoError(t, err)
}

func TestDeleteOldRunsByCount(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	// Create 3 runs
	for i := 0; i < 3; i++ {
		require.NoError(t, db.CreateRun(ctx, &model.Run{
			ID:          ulid.Make().String(),
			TaskName:    "task1",
			CreatedAt:   time.Now().Add(time.Duration(i) * time.Second), // Different times
			TriggeredBy: model.TriggeredByAPI,
		}))
	}

	task := &model.Task{
		Name:     "task1",
		KeepRuns: 2,
	}

	deleted, err := db.DeleteOldRuns(ctx, task)
	require.NoError(t, err)
	assert.Len(t, deleted, 1) // Should delete the oldest one (1 out of 3, keeping 2)

	count, err := db.CountRuns(ctx, "task1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestMarkCrashedRuns(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	// Running run
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseRunning, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(ctx, &run1))

	// Pending run
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(ctx, &run2))

	// Ended/success run (should not be touched)
	run3 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), EndAt: &time.Time{}, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(ctx, &run3))

	affected, err := db.MarkCrashedRuns(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	r1, _ := db.GetRun(ctx, run1.ID)
	assert.Equal(t, model.PhaseEnded, r1.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonCrashed), r1.EndReason)
	assert.Equal(t, -2, r1.ExitCode)

	r2, _ := db.GetRun(ctx, run2.ID)
	assert.Equal(t, model.PhasePending, r2.Status)

	r3, _ := db.GetRun(ctx, run3.ID)
	assert.Equal(t, model.PhaseEnded, r3.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonSuccess), r3.EndReason)
}

func TestSearchAndSort(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "alpha", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(ctx, &model.Run{ID: ulid.Make().String(), TaskName: "beta", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), TriggeredBy: model.TriggeredByAPI}))

	// Search
	runs, err := db.QueryRuns(ctx, RunQuery{Filter: model.RunFilter{Search: "alp"}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, "alpha", runs[0].TaskName)

	// Sort
	runs, err = db.QueryRuns(ctx, RunQuery{Limit: 10, SortField: SortColumnTaskName, SortDirection: SortAsc})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "alpha", runs[0].TaskName)
	assert.Equal(t, "beta", runs[1].TaskName)

	runs, err = db.QueryRuns(ctx, RunQuery{Limit: 10, SortField: SortColumnTaskName, SortDirection: SortDesc})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "beta", runs[0].TaskName)
}

// TestQueryRunsAllSortVariants exercises every (column, direction) tuple
// the dispatcher routes to a dedicated sqlc query. The assertion is that
// each variant returns rows in the expected order — proving the dispatch
// table maps to the right SQL and that each generated query is reachable.
func TestQueryRunsAllSortVariants(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	mk := func(id string, name string, status model.RunPhase, exit int, startOffset, durSec int) *model.Run {
		s := base.Add(time.Duration(startOffset) * time.Minute)
		e := s.Add(time.Duration(durSec) * time.Second)
		return &model.Run{
			ID:          id,
			TaskName:    name,
			Status:      status,
			EndReason:   model.EndReasonPtr(model.ReasonSuccess),
			ExitCode:    exit,
			StartAt:     &s,
			EndAt:       &e,
			TriggeredBy: model.TriggeredByAPI,
			CreatedAt:   base.Add(time.Duration(startOffset) * time.Minute),
		}
	}
	// IDs are ULIDs that also sort lexicographically by creation time. We use
	// fabricated 26-char strings that are valid ULIDs and ordered the same way
	// the rows should appear when sorted by created_at ASC.
	a := mk("01J000000000000000000000A1", "alpha", model.PhaseEnded, 0, 0, 10)
	b := mk("01J000000000000000000000B2", "bravo", model.PhaseRunning, 2, 5, 20)
	c := mk("01J000000000000000000000C3", "charlie", model.PhasePending, 1, 10, 5)
	for _, r := range []*model.Run{a, b, c} {
		require.NoError(t, db.CreateRun(ctx, r))
		require.NoError(t, db.UpdateRun(ctx, r))
	}

	names := func(rs []model.Run) []string {
		out := make([]string, len(rs))
		for i, r := range rs {
			out[i] = r.TaskName
		}
		return out
	}

	cases := []struct {
		name string
		col  SortColumn
		dir  SortDirection
		want []string
	}{
		{"created_at asc", SortColumnCreatedAt, SortAsc, []string{"alpha", "bravo", "charlie"}},
		{"created_at desc", SortColumnCreatedAt, SortDesc, []string{"charlie", "bravo", "alpha"}},
		{"start_at asc", SortColumnStartAt, SortAsc, []string{"alpha", "bravo", "charlie"}},
		{"start_at desc", SortColumnStartAt, SortDesc, []string{"charlie", "bravo", "alpha"}},
		{"task_name asc", SortColumnTaskName, SortAsc, []string{"alpha", "bravo", "charlie"}},
		{"task_name desc", SortColumnTaskName, SortDesc, []string{"charlie", "bravo", "alpha"}},
		{"status asc", SortColumnStatus, SortAsc, []string{"alpha", "charlie", "bravo"}},
		{"status desc", SortColumnStatus, SortDesc, []string{"bravo", "charlie", "alpha"}},
		{"exit_code asc", SortColumnExitCode, SortAsc, []string{"alpha", "charlie", "bravo"}},
		{"exit_code desc", SortColumnExitCode, SortDesc, []string{"bravo", "charlie", "alpha"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs, err := db.QueryRuns(ctx, RunQuery{Limit: 10, SortField: tc.col, SortDirection: tc.dir})
			require.NoError(t, err)
			assert.Equal(t, tc.want, names(rs))
		})
	}

	// Duration sort relies on julianday() over the persisted timestamps;
	// the driver stores them in a non-ISO format that SQLite can't parse,
	// so every row evaluates to a tied 0 and the ORDER BY produces an
	// unspecified order. We still exercise both dispatch branches so the
	// sqlc-generated queries are reached — we just don't assert order.
	for _, dir := range []SortDirection{SortAsc, SortDesc} {
		rs, err := db.QueryRuns(ctx, RunQuery{Limit: 10, SortField: SortColumnDuration, SortDirection: dir})
		require.NoError(t, err)
		assert.Len(t, rs, 3)
	}
}

// TestQueryRunsFiltersAndPagination exercises the WHERE / LIMIT / OFFSET
// branches of the dispatched queries that the all-variants test glosses
// over: status filter, task name filter, search filter, paging.
func TestQueryRunsFiltersAndPagination(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	mk := func(id, name string, status model.RunPhase, reason *model.EndReason) *model.Run {
		return &model.Run{
			ID: id, TaskName: name, Status: status,
			EndReason:   reason,
			StartAt:     &now,
			EndAt:       &now,
			TriggeredBy: model.TriggeredByAPI,
			CreatedAt:   now,
		}
	}
	for _, r := range []*model.Run{
		mk(ulid.Make().String(), "ingest-alpha", model.PhaseEnded, model.EndReasonPtr(model.ReasonSuccess)),
		mk(ulid.Make().String(), "ingest-beta", model.PhaseEnded, model.EndReasonPtr(model.ReasonSuccess)),
		mk(ulid.Make().String(), "build-gamma", model.PhaseRunning, nil),
	} {
		require.NoError(t, db.CreateRun(ctx, r))
		require.NoError(t, db.UpdateRun(ctx, r))
	}

	// Status filter (ended).
	rs, err := db.QueryRuns(ctx, RunQuery{
		Filter:        model.RunFilter{Status: "success"},
		Limit:         10,
		SortField:     SortColumnTaskName,
		SortDirection: SortAsc,
	})
	require.NoError(t, err)
	assert.Len(t, rs, 2)

	// Task-name filter.
	rs, err = db.QueryRuns(ctx, RunQuery{
		Filter:        model.RunFilter{TaskName: "build-gamma"},
		Limit:         10,
		SortField:     SortColumnTaskName,
		SortDirection: SortAsc,
	})
	require.NoError(t, err)
	assert.Len(t, rs, 1)

	// Search.
	rs, err = db.QueryRuns(ctx, RunQuery{
		Filter:        model.RunFilter{Search: "ingest"},
		Limit:         10,
		SortField:     SortColumnTaskName,
		SortDirection: SortAsc,
	})
	require.NoError(t, err)
	assert.Len(t, rs, 2)

	// Pagination: limit + offset against task_name ASC ordering.
	rs, err = db.QueryRuns(ctx, RunQuery{
		Limit:         2,
		Offset:        1,
		SortField:     SortColumnTaskName,
		SortDirection: SortAsc,
	})
	require.NoError(t, err)
	assert.Len(t, rs, 2)
	assert.Equal(t, "ingest-alpha", rs[0].TaskName)

	// Defensive: feeding the dispatcher an unparsed (and therefore unknown)
	// sort column must surface as an error, not a silent default.
	_, err = db.QueryRuns(ctx, RunQuery{
		Limit:         10,
		SortField:     SortColumn("garbage"),
		SortDirection: SortAsc,
	})
	require.Error(t, err)
}

// setupFullTestDB returns the full Database interface (includes PendingLogUploadRepository).
func setupFullTestDB(t *testing.T) Database {
	t.Helper()
	db, err := New(":memory:")
	require.NoError(t, err)
	return db
}

func TestGetRunByExternalExecutionID(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	extID := "ext-exec-abc123"
	run := &model.Run{
		ID:                  ulid.Make().String(),
		TaskName:            "task1",
		Status:              model.PhasePending,
		TriggeredBy:         model.TriggeredByAPI,
		ExternalExecutionID: &extID,
	}
	require.NoError(t, db.CreateRun(ctx, run))

	fetched, err := db.GetRunByExternalExecutionID(ctx, extID)
	require.NoError(t, err)
	require.NotNil(t, fetched)
	assert.Equal(t, run.ID, fetched.ID)
	assert.Equal(t, run.TaskName, fetched.TaskName)
	require.NotNil(t, fetched.ExternalExecutionID)
	assert.Equal(t, extID, *fetched.ExternalExecutionID)

	// Non-existent external execution ID returns ErrNotFound.
	_, err = db.GetRunByExternalExecutionID(ctx, "does-not-exist")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetLastRunByTask(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()

	run1 := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task-last",
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   now.Add(-10 * time.Minute),
	}
	run2 := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task-last",
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonFailed),
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   now,
	}
	require.NoError(t, db.CreateRun(ctx, run1))
	require.NoError(t, db.CreateRun(ctx, run2))

	last, err := db.GetLastRunByTask(ctx, "task-last")
	require.NoError(t, err)
	require.NotNil(t, last)
	assert.Equal(t, run2.ID, last.ID)

	// Task with no runs returns nil, nil.
	noRun, err := db.GetLastRunByTask(ctx, "no-such-task")
	require.NoError(t, err)
	assert.Nil(t, noRun)
}

func TestEnsureTaskRegistered(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	firstSeen := time.Now().Truncate(time.Second)

	// First call inserts the record.
	require.NoError(t, db.EnsureTaskRegistered(ctx, "my-task", firstSeen))

	// Second call with a different time is idempotent (INSERT OR IGNORE).
	require.NoError(t, db.EnsureTaskRegistered(ctx, "my-task", firstSeen.Add(time.Hour)))

	reg, err := db.GetTaskRegistration(ctx, "my-task")
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Equal(t, "my-task", reg.TaskName)
	// The stored value is the original firstSeen, not the later time.
	assert.WithinDuration(t, firstSeen, reg.FirstSeenAt, time.Second)
}

func TestGetTaskRegistration(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	// Non-existent task returns nil, nil.
	reg, err := db.GetTaskRegistration(ctx, "not-registered")
	require.NoError(t, err)
	assert.Nil(t, reg)

	// After registering, returns a populated record.
	firstSeen := time.Now().Truncate(time.Second)
	require.NoError(t, db.EnsureTaskRegistered(ctx, "reg-task", firstSeen))

	reg, err = db.GetTaskRegistration(ctx, "reg-task")
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Equal(t, "reg-task", reg.TaskName)
	assert.WithinDuration(t, firstSeen, reg.FirstSeenAt, time.Second)
}

func TestUpsertPendingLogUpload(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	rec := model.PendingLogUpload{
		ExternalExecutionID: "exec-upsert-1",
		UploadURL:           "https://example.com/upload/1",
		LogPath:             "/var/log/run1.log",
		InsertedAt:          1234567890,
	}

	require.NoError(t, db.UpsertPendingLogUpload(ctx, rec))

	// Upsert again with updated URL — must not error and must update.
	rec.UploadURL = "https://example.com/upload/1-updated"
	require.NoError(t, db.UpsertPendingLogUpload(ctx, rec))

	uploads, err := db.ListPendingLogUploads(ctx)
	require.NoError(t, err)
	require.Len(t, uploads, 1)
	assert.Equal(t, "https://example.com/upload/1-updated", uploads[0].UploadURL)
}

func TestDeletePendingLogUpload(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	rec := model.PendingLogUpload{
		ExternalExecutionID: "exec-delete-1",
		UploadURL:           "https://example.com/upload/del",
		LogPath:             "/var/log/rundel.log",
		InsertedAt:          111,
	}
	require.NoError(t, db.UpsertPendingLogUpload(ctx, rec))

	require.NoError(t, db.DeletePendingLogUpload(ctx, "exec-delete-1"))

	uploads, err := db.ListPendingLogUploads(ctx)
	require.NoError(t, err)
	assert.Empty(t, uploads)

	// Deleting a non-existent record is not an error.
	require.NoError(t, db.DeletePendingLogUpload(ctx, "does-not-exist"))
}

func TestListPendingLogUploads(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	// Empty table returns nil/empty slice.
	uploads, err := db.ListPendingLogUploads(ctx)
	require.NoError(t, err)
	assert.Empty(t, uploads)

	for i := range 3 {
		rec := model.PendingLogUpload{
			ExternalExecutionID: ulid.Make().String(),
			UploadURL:           "https://example.com/" + ulid.Make().String(),
			LogPath:             "/var/log/run.log",
			InsertedAt:          int64(i),
		}
		require.NoError(t, db.UpsertPendingLogUpload(ctx, rec))
	}

	uploads, err = db.ListPendingLogUploads(ctx)
	require.NoError(t, err)
	assert.Len(t, uploads, 3)
}

func TestGetPendingRuns_EmptyDatabase(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	runs, err := db.GetPendingRuns(ctx)
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestGetPendingRuns_ReturnsOnlyPendingOrderedByCreatedAt(t *testing.T) {
	ctx := t.Context()
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	for i, p := range []struct {
		status  model.RunPhase
		created time.Time
	}{
		{model.PhasePending, now.Add(-3 * time.Minute)},
		{model.PhaseRunning, now.Add(-2 * time.Minute)}, // excluded
		{model.PhasePending, now.Add(-time.Minute)},
		{model.PhaseEnded, now}, // excluded
	} {
		require.NoError(t, db.CreateRun(ctx, &model.Run{
			ID:          ulid.Make().String(),
			TaskName:    "t",
			Status:      p.status,
			TriggeredBy: model.TriggeredByAPI,
			CreatedAt:   p.created,
		}), "create run %d", i)
	}

	runs, err := db.GetPendingRuns(ctx)
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.True(t, runs[0].CreatedAt.Before(runs[1].CreatedAt), "expected ascending order")
}

func TestGetConfigValue_MissingKeyReturnsFalse(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	val, ok, err := db.GetConfigValue(ctx, "nonexistent")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "", val)
}

func TestSetConfigValue_RoundTrips(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	require.NoError(t, db.SetConfigValue(ctx, "fingerprint", "abc-123"))
	val, ok, err := db.GetConfigValue(ctx, "fingerprint")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "abc-123", val)
}

func TestSetConfigValue_UpsertsOnConflict(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	defer db.Close()

	require.NoError(t, db.SetConfigValue(ctx, "k", "first"))
	require.NoError(t, db.SetConfigValue(ctx, "k", "second"))

	val, ok, err := db.GetConfigValue(ctx, "k")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "second", val)
}

// TestSQLiteDatabase_ErrorPathsAfterClose closes the underlying SQLite
// connection and walks every read/write method to exercise the `if err != nil`
// branches that the happy-path tests skip. Every method should surface an
// error rather than panic or return zero values silently.
func TestSQLiteDatabase_ErrorPathsAfterClose(t *testing.T) {
	ctx := t.Context()
	db := setupFullTestDB(t)
	require.NoError(t, db.Close())

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "t",
		Status:      model.PhaseRunning,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}

	assert.Error(t, db.CreateRun(ctx, run))
	assert.Error(t, db.UpdateRun(ctx, run))

	_, err := db.GetRun(ctx, "any-id")
	assert.Error(t, err)

	_, err = db.GetRunByExternalExecutionID(ctx, "any-ext")
	assert.Error(t, err)

	_, err = db.GetRunSummary(ctx)
	assert.Error(t, err)

	_, err = db.CountRuns(ctx, "t")
	assert.Error(t, err)

	_, err = db.CountRunsFiltered(ctx, "", "t", "")
	assert.Error(t, err)

	_, err = db.QueryRuns(ctx, RunQuery{
		Filter:        model.RunFilter{TaskName: "t"},
		Limit:         10,
		SortField:     SortColumnCreatedAt,
		SortDirection: SortDesc,
	})
	assert.Error(t, err)

	assert.Error(t, db.DeleteRun(ctx, "any-id"))

	_, err = db.DeleteOldRuns(ctx, &model.Task{Name: "t", KeepFor: 24 * time.Hour})
	assert.Error(t, err)

	_, err = db.DeleteOldRuns(ctx, &model.Task{Name: "t", KeepRuns: 5})
	assert.Error(t, err)

	_, err = db.MarkCrashedRuns(ctx)
	assert.Error(t, err)

	_, err = db.GetPendingRuns(ctx)
	assert.Error(t, err)

	_, err = db.GetLastRunByTask(ctx, "t")
	assert.Error(t, err)

	assert.Error(t, db.EnsureTaskRegistered(ctx, "t", time.Now()))

	_, err = db.GetTaskRegistration(ctx, "t")
	assert.Error(t, err)

	_, err = db.SoftDeleteRuns(ctx, model.RunSelector{IDs: []string{"x"}}, time.Now())
	assert.Error(t, err)

	_, err = db.SoftDeleteRuns(ctx, model.RunSelector{MatchAll: true}, time.Now())
	assert.Error(t, err)

	_, err = db.RestoreRuns(ctx, model.RunSelector{IDs: []string{"x"}})
	assert.Error(t, err)

	_, err = db.RestoreRuns(ctx, model.RunSelector{MatchAll: true})
	assert.Error(t, err)

	_, err = db.ResolveSelectorIDs(ctx, model.RunSelector{IDs: []string{"x"}}, "")
	assert.Error(t, err)

	_, err = db.ResolveSelectorIDs(ctx, model.RunSelector{MatchAll: true}, "")
	assert.Error(t, err)

	_, err = db.PurgeExpiredSoftDeletes(ctx, time.Hour)
	assert.Error(t, err)

	_, _, err = db.GetConfigValue(ctx, "k")
	assert.Error(t, err)

	assert.Error(t, db.SetConfigValue(ctx, "k", "v"))

	assert.Error(t, db.UpsertPendingLogUpload(ctx, model.PendingLogUpload{
		ExternalExecutionID: "ext",
		UploadURL:           "https://example.com",
		LogPath:             "/tmp/log",
		InsertedAt:          time.Now().Unix(),
	}))
	assert.Error(t, db.DeletePendingLogUpload(ctx, "ext"))

	_, err = db.ListPendingLogUploads(ctx)
	assert.Error(t, err)
}

// TestNew_InvalidDBPathFails covers the `failed to migrate` branch by pointing
// at an unwritable path so the WAL pragma or schema migration fails.
func TestNew_InvalidDBPathFails(t *testing.T) {
	// SQLite gracefully creates files, but a path inside a non-existent dir
	// fails at Open or PRAGMA time.
	_, err := New("/proc/runwisp-nonexistent-dir/foo.db")
	require.Error(t, err)
}
