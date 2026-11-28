// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type failingDeleteRepo struct {
	storage.RunRepository
	deleteErr error
}

func (f *failingDeleteRepo) DeleteRunsByIDs(ctx context.Context, ids []string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.RunRepository.DeleteRunsByIDs(ctx, ids)
}

func TestRetentionCleaner(t *testing.T) {
	repo := new(testutil.MockRunRepository)

	logDir := t.TempDir()

	runID := ulid.Make().String()
	now := time.Now()

	task := &model.Task{
		Name:    "task1",
		KeepFor: 24 * time.Hour,
	}
	tasks := map[string]*model.Task{"task1": task}

	// Create the log file at the computed path
	logPath := logutil.ResolveRunLogPath(logDir, "task1", runID, now)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	require.NoError(t, os.WriteFile(logPath, []byte("log data"), 0644))

	oldRuns := []model.Run{
		{ID: runID, TaskName: "task1", CreatedAt: now},
	}

	repo.On("ListPendingRetentionDeletes", mock.Anything).Return([]storage.RunRef(nil), nil)
	expectRetentionClaim(repo, runID, "task1", now)
	repo.On("SelectOldRuns", mock.Anything, task).Return(oldRuns, nil)
	repo.On("DeleteRunsByIDs", mock.Anything, []string{runID}).Return(nil)

	bus := events.NewEventBus()
	var published []events.RunDeletedEvent
	bus.Subscribe(events.EventRunDeleted, func(e events.Event) {
		published = append(published, e.Data.(events.RunDeletedEvent))
	})

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(tasks), time.Hour, logDir, 0, bus)
	// Start runs the first cleanup pass synchronously, so the deletion is
	// observable as soon as Start returns — no sleep needed.
	cleaner.Start()
	defer cleaner.Stop()

	repo.AssertExpectations(t)

	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))

	// A dashboard tracking totalRuns via run.deleted must hear about a run
	// retention pruned, or the counter only drifts back into sync on reload.
	require.Len(t, published, 1)
	assert.Equal(t, runID, published[0].RunID)
	assert.Equal(t, "task1", published[0].TaskName)
}

func TestEnforceMaxTotalSize_NoLimit(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	// maxTotalSize=0 means no limit; enforceMaxTotalSize should be a no-op
	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 0, events.NewEventBus())
	cleaner.enforceMaxTotalSize(context.Background()) // must not call QueryRuns
	repo.AssertNotCalled(t, "QueryRuns")
}

func TestEnforceMaxTotalSize_UnderLimit(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	// Write a tiny file; maxTotalSize is huge → no pruning
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "small.log"), []byte("x"), 0644))
	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 1<<30, events.NewEventBus()) // 1 GiB
	cleaner.enforceMaxTotalSize(context.Background())
	repo.AssertNotCalled(t, "QueryRuns")
}

// A run's rotated-away .prev segment is deleted alongside its .log and counted
// by the initial dirSize, so its size must be subtracted from the running
// total too. Otherwise the tracker stays high and prunes more runs than the
// cap requires. Regression: two runs, each 100B log + 100B prev (400B total,
// 250B cap) — deleting the oldest run frees 200B and drops under the cap, so
// the second run must survive.
func TestEnforceMaxTotalSize_SubtractsPrevSegment(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	now := time.Now()

	makeRun := func(createdAt time.Time) model.Run {
		id := ulid.Make().String()
		logPath := logutil.ResolveRunLogPath(logDir, "task1", id, createdAt)
		require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
		require.NoError(t, os.WriteFile(logPath, make([]byte, 100), 0644))
		require.NoError(t, os.WriteFile(logutil.PrevPath(logPath), make([]byte, 100), 0644))
		return model.Run{ID: id, TaskName: "task1", CreatedAt: createdAt, Status: model.PhaseEnded}
	}

	oldest := makeRun(now.Add(-time.Hour))
	newest := makeRun(now)

	enforceQuery := storage.RunQuery{
		Limit:         100,
		Offset:        0,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortAsc,
	}
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{oldest, newest}, nil)
	expectRetentionClaim(repo, oldest.ID, oldest.TaskName, oldest.CreatedAt)
	repo.On("DeleteRunsByIDs", mock.Anything, []string{oldest.ID}).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 250, events.NewEventBus())
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRunsByIDs", mock.Anything, []string{oldest.ID})
	repo.AssertNotCalled(t, "DeleteRunsByIDs", mock.Anything, []string{newest.ID})
}

// The log file must be removed before the DB row is deleted — a lingering
// log-less "ghost" row is treated as benign (indistinguishable from a
// legitimate no-log run) while an orphaned log file is an unidentifiable,
// unreclaimable disk leak that stays counted against the size cap forever.
// cleanOldRuns and softdelete_purger.purge follow the same log-then-row
// ordering (see TestCleanOldRuns_LogFileRemovedBeforeRowOnDeleteFailure and
// TestPurge_LogFileRemovedBeforeRowOnDeleteFailure) — keep all three in sync.
func TestEnforceMaxTotalSize_DeletesLogFileBeforeDBRow(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	now := time.Now()

	runID := ulid.Make().String()
	logPath := logutil.ResolveRunLogPath(logDir, "task1", runID, now)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	require.NoError(t, os.WriteFile(logPath, make([]byte, 200), 0644))

	run := model.Run{ID: runID, TaskName: "task1", CreatedAt: now, Status: model.PhaseEnded}

	enforceQuery := storage.RunQuery{
		Limit:         100,
		Offset:        0,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortAsc,
	}
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{run}, nil).Once()
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{}, nil)
	expectRetentionClaim(repo, runID, run.TaskName, run.CreatedAt)
	repo.On("DeleteRunsByIDs", mock.Anything, []string{runID}).Run(func(mock.Arguments) {
		// The log file must already be gone by the time the DB delete happens.
		_, err := os.Stat(logPath)
		assert.True(t, os.IsNotExist(err), "log file must already be removed when DeleteRunsByIDs is called")
	}).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 100, events.NewEventBus())
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRunsByIDs", mock.Anything, []string{runID})
}

func TestEnforceMaxTotalSize_PrunesOldestTerminalRun(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	now := time.Now()

	runID := ulid.Make().String()
	logPath := logutil.ResolveRunLogPath(logDir, "task1", runID, now)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	// Write 200 bytes; set limit to 100 bytes to trigger pruning
	require.NoError(t, os.WriteFile(logPath, make([]byte, 200), 0644))

	run := model.Run{ID: runID, TaskName: "task1", CreatedAt: now, Status: model.PhaseEnded}

	enforceQuery := storage.RunQuery{
		Limit:         100,
		Offset:        0,
		SortField:     storage.SortColumnCreatedAt,
		SortDirection: storage.SortAsc,
	}
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{run}, nil)
	// Second call returns empty to stop the loop
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{}, nil)
	expectRetentionClaim(repo, runID, run.TaskName, run.CreatedAt)
	repo.On("DeleteRunsByIDs", mock.Anything, []string{runID}).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 100, events.NewEventBus())
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRunsByIDs", mock.Anything, []string{runID})
}

// When a running run's log and a file no run owns already exceed the cap,
// deleting every finished run still can't get under it, so history must stay.
func TestEnforceMaxTotalSize_KeepsHistoryWhenDeletingCannotReachCap(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	now := time.Now()

	writeLog := func(id string, size int) {
		logPath := logutil.ResolveRunLogPath(logDir, "task1", id, now)
		require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
		require.NoError(t, os.WriteFile(logPath, make([]byte, size), 0644))
	}
	active := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-2 * time.Hour), Status: model.PhaseRunning}
	done := model.Run{ID: ulid.Make().String(), TaskName: "task1", CreatedAt: now.Add(-time.Hour), Status: model.PhaseEnded}
	writeLog(active.ID, 500)
	writeLog(done.ID, 100)
	writeLog(ulid.Make().String(), 500) // unaccounted: no run row owns it

	enforceQuery := storage.RunQuery{Limit: 100, SortField: storage.SortColumnCreatedAt, SortDirection: storage.SortAsc}
	repo.On("QueryRuns", mock.Anything, enforceQuery).Return([]model.Run{active, done}, nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 200, events.NewEventBus())
	assert.Zero(t, cleaner.enforceMaxTotalSize(context.Background()))

	repo.AssertNotCalled(t, "DeleteRunsByIDs", mock.Anything, mock.Anything)
}

func expectRetentionClaim(repo *testutil.MockRunRepository, id, task string, createdAt time.Time) {
	repo.On("ClaimRetentionDeletes", mock.Anything, []string{id}).
		Return([]storage.RunRef{{ID: id, TaskName: task, CreatedAt: createdAt}}, nil)
}

func TestRetentionRetriesFailedRowDeletionWithoutReportingSuccess(t *testing.T) {
	ctx := t.Context()
	logDir := t.TempDir()

	realDB, err := storage.New(":memory:")
	require.NoError(t, err)
	defer realDB.Close()

	now := time.Now()
	old := now.Add(-25 * time.Hour)
	run := model.Run{
		ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded,
		EndReason: model.EndReasonPtr(model.ReasonSuccess), CreatedAt: old, TriggeredBy: model.TriggeredByAPI,
	}
	require.NoError(t, realDB.CreateRun(ctx, &run))

	logPath := logutil.ResolveRunLogPath(logDir, "task1", run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0755))
	require.NoError(t, os.WriteFile(logPath, []byte("log data"), 0644))

	repo := &failingDeleteRepo{RunRepository: realDB, deleteErr: errors.New("delete boom")}
	task := &model.Task{Name: "task1", KeepFor: 24 * time.Hour}
	registry := NewTaskRegistry(map[string]*model.Task{"task1": task})
	bus := events.NewEventBus()
	published := 0
	bus.Subscribe(events.EventRunDeleted, func(events.Event) { published++ })
	cleaner := NewRetentionCleaner(repo, registry, time.Hour, logDir, 0, bus)

	var messages bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&messages, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	cleaner.cleanOldRuns(ctx)
	assert.NoFileExists(t, logPath)
	_, err = realDB.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.Zero(t, published)
	assert.Contains(t, messages.String(), "Failed to delete run rows")
	assert.NotContains(t, messages.String(), "Retention cleaned runs")
	assert.NotContains(t, messages.String(), "Retention cleanup complete")

	// Resume cleanup even if the daemon no longer has a retention rule for the task.
	registry.Delete(task.Name)
	repo.deleteErr = nil
	cleaner.cleanOldRuns(ctx)
	_, err = realDB.GetRun(ctx, run.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.Equal(t, 1, published)
	assert.Contains(t, messages.String(), "Retention cleanup complete")
}

func TestSizeRetentionRetriesFailedLogRemoval(t *testing.T) {
	ctx := t.Context()
	logDir := t.TempDir()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	run := model.Run{
		ID: ulid.Make().String(), TaskName: "task1", Status: model.PhaseEnded,
		CreatedAt: time.Now(), TriggeredBy: model.TriggeredByAPI,
	}
	require.NoError(t, db.CreateRun(ctx, &run))
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(logutil.PrevPath(logPath), 0o755))
	blocker := filepath.Join(logutil.PrevPath(logPath), "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(logPath, []byte("output"), 0o644))

	cleaner := NewRetentionCleaner(db, NewTaskRegistry(nil), time.Hour, logDir, 1, nil)
	assert.Zero(t, cleaner.enforceMaxTotalSize(ctx))
	_, err = db.GetRun(ctx, run.ID)
	require.NoError(t, err)
	assert.FileExists(t, blocker)
	assert.FileExists(t, logPath)

	require.NoError(t, os.Remove(blocker))
	cleaner.SetMaxTotalSize(0)
	cleaner.cleanOldRuns(ctx)
	_, err = db.GetRun(ctx, run.ID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	assert.NoDirExists(t, logutil.PrevPath(logPath))
}
