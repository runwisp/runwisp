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
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

type fakePendingRepo struct {
	mu      sync.Mutex
	rows    map[string]storage.PendingLogUpload
	upserts int
	deletes int
}

func newFakePendingRepo() *fakePendingRepo {
	return &fakePendingRepo{rows: map[string]storage.PendingLogUpload{}}
}

func (f *fakePendingRepo) UpsertPendingLogUpload(rec storage.PendingLogUpload) error {
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

func (f *fakePendingRepo) ListPendingLogUploads() ([]storage.PendingLogUpload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]storage.PendingLogUpload, 0, len(f.rows))
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
	byExt map[string]*model.Run
}

func (r *fakeRunRepo) GetRunByExternalExecutionID(id string) (*model.Run, error) {
	run, ok := r.byExt[id]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return run, nil
}

// The remaining storage.RunRepository methods are unused in these tests.
func (r *fakeRunRepo) CreateRun(*model.Run) error        { return nil }
func (r *fakeRunRepo) UpdateRun(*model.Run) error        { return nil }
func (r *fakeRunRepo) GetRun(string) (*model.Run, error) { return nil, storage.ErrNotFound }
func (r *fakeRunRepo) CountRuns(string) (int64, error)   { return 0, nil }
func (r *fakeRunRepo) CountRunsFiltered(string, string, string) (int64, error) {
	return 0, nil
}
func (r *fakeRunRepo) QueryRuns(string, int, int, string, string, string, string) ([]model.Run, error) {
	return nil, nil
}
func (r *fakeRunRepo) DeleteRun(string) error                         { return nil }
func (r *fakeRunRepo) DeleteOldRuns(*model.Task) ([]model.Run, error) { return nil, nil }
func (r *fakeRunRepo) MarkCrashedRuns() (int64, error)                { return 0, nil }
func (r *fakeRunRepo) GetPendingRuns() ([]model.Run, error)           { return nil, nil }
func (r *fakeRunRepo) GetLastRunByTask(string) (*model.Run, error)    { return nil, storage.ErrNotFound }
func (r *fakeRunRepo) GetRunSummary() (*model.RunSummary, error)      { return nil, nil }
func (r *fakeRunRepo) EnsureTaskRegistered(string, time.Time) error   { return nil }
func (r *fakeRunRepo) GetTaskRegistration(string) (*model.TaskRegistration, error) {
	return nil, storage.ErrNotFound
}
func (r *fakeRunRepo) Close() error { return nil }

func writeRunLog(t *testing.T, logDir string, run *model.Run, body string) string {
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

func newTerminalRun(taskName, runID, externalID string) *model.Run {
	ext := externalID
	return &model.Run{
		ID:                  runID,
		ExternalExecutionID: &ext,
		TaskName:            taskName,
		Status:              model.PhaseEnded,
		CreatedAt:           time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}
}

// --- RegisterDispatch -------------------------------------------------------

func TestRegisterDispatchPersistsAndCachesEntry(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir())

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

func TestRegisterDispatchEmptyURLIsNoop(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir())

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
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir())

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
	runs := &fakeRunRepo{byExt: map[string]*model.Run{"exec-1": run}}
	u := NewLogUploader(repo, runs, logDir)
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
	u := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, t.TempDir())

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
	u := NewLogUploader(repo, &fakeRunRepo{}, logDir)

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
	_ = repo.UpsertPendingLogUpload(storage.PendingLogUpload{
		ExternalExecutionID: "exec-gone",
		UploadURL:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{}}, t.TempDir())

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
	runningRun := &model.Run{
		ID:                  "run-1",
		ExternalExecutionID: strPtr("exec-running"),
		TaskName:            "greet",
		Status:              model.PhaseRunning,
		CreatedAt:           time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	}

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(storage.PendingLogUpload{
		ExternalExecutionID: "exec-running",
		UploadURL:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-running": runningRun}}, logDir)

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
	_ = repo.UpsertPendingLogUpload(storage.PendingLogUpload{
		ExternalExecutionID: "exec-1",
		UploadURL:           srv.URL,
		LogPath:             "key/exec-1.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-1": run}}, logDir)
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
