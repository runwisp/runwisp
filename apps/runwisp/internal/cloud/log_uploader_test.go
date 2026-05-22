// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

type fakePendingRepo struct {
	mu      sync.Mutex
	rows    map[string]sqlcdb.PendingLogUpload
	upserts int
	deletes int
}

func newFakePendingRepo() *fakePendingRepo {
	return &fakePendingRepo{rows: map[string]sqlcdb.PendingLogUpload{}}
}

func (f *fakePendingRepo) UpsertPendingLogUpload(rec sqlcdb.PendingLogUpload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[rec.ExternalExecutionID] = rec
	f.upserts++
	return nil
}

func (f *fakePendingRepo) DeletePendingLogUpload(externalExecutionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, externalExecutionID)
	f.deletes++
	return nil
}

func (f *fakePendingRepo) ListPendingLogUploads() ([]sqlcdb.PendingLogUpload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]sqlcdb.PendingLogUpload, 0, len(f.rows))
	for _, r := range f.rows {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakePendingRepo) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.rows)
}

type fakeRunRepo struct {
	byExt map[string]*sqlcdb.Run
}

func (r *fakeRunRepo) GetRunByExternalExecutionID(id string) (*sqlcdb.Run, error) {
	run, ok := r.byExt[id]
	if !ok {
		return nil, ErrNotFound
	}
	return run, nil
}

// fixedClock returns a deterministic wall-clock for log_uploader tests. The
// exact instant doesn't matter for correctness; what matters is that
// InsertedAt is reproducible run-to-run.
func fixedClock() func() time.Time {
	t := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func writeRunLog(t *testing.T, logDir string, run *sqlcdb.Run, body string) string {
	t.Helper()
	path := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

func newTerminalRun(taskName, runID, externalID string) *sqlcdb.Run {
	ext := externalID
	return &sqlcdb.Run{
		ID:                  runID,
		ExternalExecutionID: &ext,
		TaskName:            taskName,
		Status:              sqlcdb.PhaseEnded,
		CreatedAt:           time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

// --- RegisterDispatch -------------------------------------------------------

func TestRegisterDispatchPersistsAndCachesEntry(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())

	if err := u.RegisterDispatch("exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	if got := repo.count(); got != 1 {
		t.Fatalf("repo rows = %d, want 1", got)
	}
	if _, ok := u.lookup("exec-1"); !ok {
		t.Fatal("in-memory entry missing after RegisterDispatch")
	}
}

// TestRegisterDispatchUsesInjectedClock pins the determinism guarantee added
// with the LogUploader clock injection: InsertedAt must come from the
// injected clock, not time.Now(). Regressions here would let wall-clock
// drift creep into persisted dispatch records.
func TestRegisterDispatchUsesInjectedClock(t *testing.T) {
	repo := newFakePendingRepo()
	fixed := time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC)
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), func() time.Time { return fixed })

	if err := u.RegisterDispatch("exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	rows, err := repo.ListPendingLogUploads()
	if err != nil {
		t.Fatalf("ListPendingLogUploads: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if got, want := rows[0].InsertedAt, fixed.Unix(); got != want {
		t.Errorf("InsertedAt = %d, want %d (injected clock)", got, want)
	}
}

func TestRegisterDispatchEmptyURLIsNoop(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())

	if err := u.RegisterDispatch("exec-1", "", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0 (empty URL must skip persistence)", got)
	}
	if _, ok := u.lookup("exec-1"); ok {
		t.Error("in-memory entry should not be created for empty URL")
	}
}

func TestForgetRemovesRowAndCachedEntry(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())

	if err := u.RegisterDispatch("exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	u.Forget("exec-1")

	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0", got)
	}
	if _, ok := u.lookup("exec-1"); ok {
		t.Error("in-memory entry should be gone after Forget")
	}
}

// --- Archive ---------------------------------------------------------------

func TestArchiveSuccessRemovesRowAndLogFile(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN", "exec-1")
	logPath := writeRunLog(t, logDir, run, "hello\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	runs := &fakeRunRepo{byExt: map[string]*sqlcdb.Run{"exec-1": run}}
	u := NewLogUploader(repo, runs, logDir, fixedClock())
	u.httpClient = srv.Client()

	if err := u.RegisterDispatch("exec-1", srv.URL, "key/exec-1.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	result, err := u.Archive(context.Background(), "exec-1", logPath)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result on successful upload")
	}
	if result.LogPath != "key/exec-1.log.gz" {
		t.Errorf("LogPath = %q, want %q", result.LogPath, "key/exec-1.log.gz")
	}
	if result.LogSize <= 0 {
		t.Errorf("LogSize = %d, want >0", result.LogSize)
	}

	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d after success, want 0", got)
	}
	if _, ok := u.lookup("exec-1"); ok {
		t.Error("in-memory entry should be cleared after success")
	}
	if _, statErr := os.Stat(logPath); !os.IsNotExist(statErr) {
		t.Errorf("log file should be deleted after archival, stat err: %v", statErr)
	}
}

func TestArchiveNoPendingEntryIsNoop(t *testing.T) {
	u := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, t.TempDir(), fixedClock())

	result, err := u.Archive(context.Background(), "missing", "/does/not/matter")
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result for unknown executionID, got %+v", result)
	}
}

func TestArchiveMissingLogFileForgetsEntry(t *testing.T) {
	logDir := t.TempDir()
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, logDir, fixedClock())

	if err := u.RegisterDispatch("exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	missing := filepath.Join(logDir, "nope.log")
	result, err := u.Archive(context.Background(), "exec-1", missing)
	if err != nil {
		t.Fatalf("Archive returned error for missing file: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result when log file is gone, got %+v", result)
	}
	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0 (missing log file should drop the entry)", got)
	}
}

// --- RecoverOrphans --------------------------------------------------------

func TestRecoverOrphansDropsRowWhenRunMissing(t *testing.T) {
	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(sqlcdb.PendingLogUpload{
		ExternalExecutionID: "exec-gone",
		UploadUrl:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*sqlcdb.Run{}}, t.TempDir(), fixedClock())

	var emitted []string
	u.RecoverOrphans(context.Background(), func(id string, _ LogUploaderResult) {
		emitted = append(emitted, id)
	})

	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0 (orphan with missing run should be dropped)", got)
	}
	if len(emitted) != 0 {
		t.Errorf("emit invoked for missing-run orphan: %v", emitted)
	}
}

func TestRecoverOrphansSkipsNonTerminalRun(t *testing.T) {
	logDir := t.TempDir()
	runningRun := &sqlcdb.Run{
		ID:                  "run-1",
		ExternalExecutionID: strPtr("exec-running"),
		TaskName:            "greet",
		Status:              sqlcdb.PhaseRunning,
		CreatedAt:           time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(sqlcdb.PendingLogUpload{
		ExternalExecutionID: "exec-running",
		UploadUrl:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*sqlcdb.Run{"exec-running": runningRun}}, logDir, fixedClock())

	var emitted []string
	u.RecoverOrphans(context.Background(), func(id string, _ LogUploaderResult) {
		emitted = append(emitted, id)
	})

	if got := repo.count(); got != 1 {
		t.Errorf("repo rows = %d, want 1 (in-progress run should keep its row)", got)
	}
	if len(emitted) != 0 {
		t.Errorf("emit invoked for non-terminal orphan: %v", emitted)
	}
}

func TestRecoverOrphansRetriesTerminatedRun(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "run-1", "exec-1")
	writeRunLog(t, logDir, run, "tail of log\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(sqlcdb.PendingLogUpload{
		ExternalExecutionID: "exec-1",
		UploadUrl:           srv.URL,
		LogPath:             "key/exec-1.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*sqlcdb.Run{"exec-1": run}}, logDir, fixedClock())
	u.httpClient = srv.Client()

	var emitted []struct {
		id     string
		result LogUploaderResult
	}
	u.RecoverOrphans(context.Background(), func(id string, result LogUploaderResult) {
		emitted = append(emitted, struct {
			id     string
			result LogUploaderResult
		}{id, result})
	})

	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0 after successful recovery", got)
	}
	if len(emitted) != 1 || emitted[0].id != "exec-1" {
		t.Fatalf("emit calls = %+v, want exactly one for exec-1", emitted)
	}
	if emitted[0].result.LogPath != "key/exec-1.log.gz" {
		t.Errorf("emitted LogPath = %q, want %q", emitted[0].result.LogPath, "key/exec-1.log.gz")
	}
	if emitted[0].result.LogSize <= 0 {
		t.Errorf("emitted LogSize = %d, want >0", emitted[0].result.LogSize)
	}
}

func TestRecoverOrphansNilUploaderIsNoop(t *testing.T) {
	var u *LogUploader
	// Must not panic.
	u.RecoverOrphans(context.Background(), func(string, LogUploaderResult) {})
}

func strPtr(s string) *string { return &s }
