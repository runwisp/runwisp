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
	db, err := New(":memory:", nil)
	require.NoError(t, err)
	return db
}

func TestCreateAndGetRun(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}

	err := db.CreateRun(run)
	require.NoError(t, err)

	fetched, err := db.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, run.ID, fetched.ID)
	assert.Equal(t, run.TaskName, fetched.TaskName)
}

func TestUpdateRun(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "test-task",
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(run))

	run.Status = model.PhaseRunning
	err := db.UpdateRun(run)
	require.NoError(t, err)

	fetched, err := db.GetRun(run.ID)
	require.NoError(t, err)
	assert.Equal(t, model.PhaseRunning, fetched.Status)
}

func TestCountRuns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))

	count, err := db.CountRuns("task1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = db.CountRuns("task2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCountRunsFiltered(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), TriggeredBy: model.TriggeredByAPI}))

	count, err := db.CountRunsFiltered("", "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(3), count)

	count, err = db.CountRunsFiltered(string(model.ReasonSuccess), "", "")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = db.CountRunsFiltered("", "task1", "")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	count, err = db.CountRunsFiltered("", "", "task2")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestQueryRunsByTask(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))

	runs, err := db.QueryRuns("task1", 10, 0, "", "", "", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)

	runs, err = db.QueryRuns("task1", 10, 0, string(model.ReasonSuccess), "", "", "")
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, model.PhaseEnded, runs[0].Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonSuccess), runs[0].EndReason)
}

func TestQueryAllRuns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "task2", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))

	runs, err := db.QueryRuns("", 10, 0, "", "", "", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)

	runs, err = db.QueryRuns("task1", 10, 0, "", "", "", "")
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, "task1", runs[0].TaskName)
}

func TestDeleteRun(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id := ulid.Make().String()
	require.NoError(t, db.CreateRun(&model.Run{ID: id, TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}))

	err := db.DeleteRun(id)
	require.NoError(t, err)

	_, err = db.GetRun(id)
	assert.Error(t, err)
}

func TestDeleteOldRuns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now()
	old := now.Add(-25 * time.Hour)

	// Run to be deleted by hours retention (24h)
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: old, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(&run1))

	// Run to be kept
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(&run2))

	task := &model.Task{
		Name:    "task1",
		KeepFor: "1d",
	}

	deleted, err := db.DeleteOldRuns(task)
	require.NoError(t, err)
	assert.Len(t, deleted, 1)
	assert.Equal(t, run1.ID, deleted[0].ID)

	_, err = db.GetRun(run1.ID)
	assert.Error(t, err)
	_, err = db.GetRun(run2.ID)
	assert.NoError(t, err)
}

func TestDeleteOldRunsByCount(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Create 3 runs
	for i := 0; i < 3; i++ {
		require.NoError(t, db.CreateRun(&model.Run{
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

	deleted, err := db.DeleteOldRuns(task)
	require.NoError(t, err)
	assert.Len(t, deleted, 1) // Should delete the oldest one (1 out of 3, keeping 2)

	count, err := db.CountRuns("task1")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestMarkCrashedRuns(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	// Running run
	run1 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseRunning, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(&run1))

	// Pending run
	run2 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhasePending, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(&run2))

	// Ended/success run (should not be touched)
	run3 := model.Run{ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), EndAt: &time.Time{}, TriggeredBy: model.TriggeredByAPI}
	require.NoError(t, db.CreateRun(&run3))

	affected, err := db.MarkCrashedRuns()
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	r1, _ := db.GetRun(run1.ID)
	assert.Equal(t, model.PhaseEnded, r1.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonCrashed), r1.EndReason)
	assert.Equal(t, -2, r1.ExitCode)

	r2, _ := db.GetRun(run2.ID)
	assert.Equal(t, model.PhasePending, r2.Status)

	r3, _ := db.GetRun(run3.ID)
	assert.Equal(t, model.PhaseEnded, r3.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonSuccess), r3.EndReason)
}

func TestSearchAndSort(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "alpha", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonSuccess), TriggeredBy: model.TriggeredByAPI}))
	require.NoError(t, db.CreateRun(&model.Run{ID: ulid.Make().String(), TaskName: "beta", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonFailed), TriggeredBy: model.TriggeredByAPI}))

	// Search
	runs, err := db.QueryRuns("", 10, 0, "", "", "", "alp")
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, "alpha", runs[0].TaskName)

	// Sort
	runs, err = db.QueryRuns("", 10, 0, "", "task_name", "asc", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "alpha", runs[0].TaskName)
	assert.Equal(t, "beta", runs[1].TaskName)

	runs, err = db.QueryRuns("", 10, 0, "", "task_name", "desc", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "beta", runs[0].TaskName)
}
