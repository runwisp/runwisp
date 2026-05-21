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
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeRunWithLog creates a soft-deleted run and writes a fake log file at
// the path the purger will look up. Returns (runID, logPath).
func makeRunWithLog(t *testing.T, db storage.RunRepository, logDir string, deletedAt time.Time) (string, string) {
	t.Helper()
	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    "task1",
		Status:      model.PhaseEnded,
		EndReason:   model.EndReasonPtr(model.ReasonSuccess),
		TriggeredBy: model.TriggeredByAPI,
		CreatedAt:   time.Now(),
	}
	require.NoError(t, db.CreateRun(run))

	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	require.NoError(t, os.WriteFile(logPath, []byte("hello\n"), 0o644))

	_, err := db.SoftDeleteRuns(model.RunSelector{IDs: []string{run.ID}}, deletedAt)
	require.NoError(t, err)
	return run.ID, logPath
}

func TestSoftDeletePurger_DrainOnBoot(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:", nil)
	require.NoError(t, err)
	defer db.Close()

	runID, logPath := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(0) // simulate boot drain — TTL 0 ⇒ everything goes.

	// Row is hard-deleted.
	_, err = db.GetRun(runID)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Log file is gone.
	_, err = os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSoftDeletePurger_KeepsRunsInsideTTL(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:", nil)
	require.NoError(t, err)
	defer db.Close()

	runID, logPath := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(time.Hour) // TTL way bigger than how long the run has been deleted.

	// Row should still be present (restorable).
	restored, err := db.RestoreRuns(model.RunSelector{IDs: []string{runID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)

	// Log file is still there.
	_, err = os.Stat(logPath)
	require.NoError(t, err)
}

func TestSoftDeletePurger_RemovesExpiredButKeepsFresh(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:", nil)
	require.NoError(t, err)
	defer db.Close()

	oldID, oldLog := makeRunWithLog(t, db, logDir, time.Now().Add(-time.Hour))
	freshID, freshLog := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(time.Minute) // old run is past it; fresh is well inside.

	_, err = db.GetRun(oldID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = os.Stat(oldLog)
	assert.True(t, os.IsNotExist(err))

	// Fresh remains in deleted state, restorable.
	restored, err := db.RestoreRuns(model.RunSelector{IDs: []string{freshID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	_, err = os.Stat(freshLog)
	require.NoError(t, err)
}
