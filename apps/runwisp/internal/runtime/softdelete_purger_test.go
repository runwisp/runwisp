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
	require.NoError(t, db.CreateRun(context.Background(), run))

	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(logPath), 0o755))
	require.NoError(t, os.WriteFile(logPath, []byte("hello\n"), 0o644))

	_, err := db.SoftDeleteRuns(context.Background(), model.RunSelector{IDs: []string{run.ID}}, deletedAt)
	require.NoError(t, err)
	return run.ID, logPath
}

func TestSoftDeletePurger_DrainOnBoot(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	runID, logPath := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(context.Background(), 0) // simulate boot drain — TTL 0 ⇒ everything goes.

	// Row is hard-deleted.
	_, err = db.GetRun(context.Background(), runID)
	assert.ErrorIs(t, err, storage.ErrNotFound)

	// Log file is gone.
	_, err = os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))
}

func TestSoftDeletePurger_KeepsRunsInsideTTL(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	runID, logPath := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(context.Background(), time.Hour) // TTL way bigger than how long the run has been deleted.

	// Row should still be present (restorable).
	restored, err := db.RestoreRuns(context.Background(), model.RunSelector{IDs: []string{runID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)

	// Log file is still there.
	_, err = os.Stat(logPath)
	require.NoError(t, err)
}

func TestSoftDeletePurger_StartAndStop(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	// Pre-seed a soft-deleted run with an old deleted_at so the boot drain
	// reclaims it immediately on Start.
	runID, logPath := makeRunWithLog(t, db, logDir, time.Now().Add(-time.Hour))

	p := NewSoftDeletePurger(db, logDir)
	p.Start()
	t.Cleanup(p.Stop)

	// Boot drain ran synchronously inside Start, so the row should already be gone.
	_, err = db.GetRun(t.Context(), runID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = os.Stat(logPath)
	assert.True(t, os.IsNotExist(err))

	// Calling Stop twice (or after a no-Start construct) must be safe.
	p.Stop()

	other := NewSoftDeletePurger(db, logDir)
	assert.NotPanics(t, func() { other.Stop() })
}

func TestSoftDeletePurger_RemovesExpiredButKeepsFresh(t *testing.T) {
	logDir := t.TempDir()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	oldID, oldLog := makeRunWithLog(t, db, logDir, time.Now().Add(-time.Hour))
	freshID, freshLog := makeRunWithLog(t, db, logDir, time.Now())

	p := NewSoftDeletePurger(db, logDir)
	p.purge(context.Background(), time.Minute) // old run is past it; fresh is well inside.

	_, err = db.GetRun(context.Background(), oldID)
	assert.ErrorIs(t, err, storage.ErrNotFound)
	_, err = os.Stat(oldLog)
	assert.True(t, os.IsNotExist(err))

	// Fresh remains in deleted state, restorable.
	restored, err := db.RestoreRuns(context.Background(), model.RunSelector{IDs: []string{freshID}})
	require.NoError(t, err)
	require.Len(t, restored, 1)
	_, err = os.Stat(freshLog)
	require.NoError(t, err)
}
