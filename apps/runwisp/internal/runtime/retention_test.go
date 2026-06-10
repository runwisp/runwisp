// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
