// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

	deletedRuns := []model.Run{
		{ID: runID, TaskName: "task1", CreatedAt: now},
	}

	repo.On("DeleteOldRuns", mock.Anything, task).Return(deletedRuns, nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(tasks), 10*time.Millisecond, logDir, 0)
	// Start runs the first cleanup pass synchronously, so the deletion is
	// observable as soon as Start returns — no sleep needed.
	cleaner.Start()
	defer cleaner.Stop()

	repo.AssertExpectations(t)

	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))
}

func TestEnforceMaxTotalSize_NoLimit(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	// maxTotalSize=0 means no limit; enforceMaxTotalSize should be a no-op
	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 0)
	cleaner.enforceMaxTotalSize(context.Background()) // must not call QueryRuns
	repo.AssertNotCalled(t, "QueryRuns")
}

func TestEnforceMaxTotalSize_UnderLimit(t *testing.T) {
	repo := new(testutil.MockRunRepository)
	logDir := t.TempDir()
	// Write a tiny file; maxTotalSize is huge → no pruning
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "small.log"), []byte("x"), 0644))
	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 1<<30) // 1 GiB
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
	repo.On("DeleteRun", mock.Anything, oldest.ID).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 250)
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRun", mock.Anything, oldest.ID)
	repo.AssertNotCalled(t, "DeleteRun", mock.Anything, newest.ID)
}

// The DB row must be deleted before the log file is removed from disk — a
// SIGKILL between the two steps should leave an orphaned log file (harmless)
// rather than a DB row pointing at a deleted log. Assert order by checking
// the log file still exists at the moment DeleteRun is invoked, and is gone
// only afterward.
func TestEnforceMaxTotalSize_DeletesDBRowBeforeLogFile(t *testing.T) {
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
	repo.On("DeleteRun", mock.Anything, runID).Run(func(mock.Arguments) {
		// The log file must still be on disk when the DB delete happens.
		_, err := os.Stat(logPath)
		assert.NoError(t, err, "log file must still exist when DeleteRun is called")
	}).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 100)
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRun", mock.Anything, runID)
	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err), "log file must be removed after DeleteRun succeeds")
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
	repo.On("DeleteRun", mock.Anything, runID).Return(nil)

	cleaner := NewRetentionCleaner(repo, NewTaskRegistry(nil), time.Hour, logDir, 100)
	cleaner.enforceMaxTotalSize(context.Background())

	repo.AssertCalled(t, "DeleteRun", mock.Anything, runID)
}
