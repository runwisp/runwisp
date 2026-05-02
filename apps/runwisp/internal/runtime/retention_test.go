// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
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

	repo.On("DeleteOldRuns", task).Return(deletedRuns, nil)

	cleaner := NewRetentionCleaner(repo, tasks, 10*time.Millisecond, logDir, 0)
	cleaner.Start()
	defer cleaner.Stop()

	time.Sleep(50 * time.Millisecond)

	repo.AssertExpectations(t)

	_, err := os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))
}
