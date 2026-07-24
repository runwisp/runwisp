// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

type fakePendingRepo struct {
	mu      sync.Mutex
	rows    map[string]model.PendingLogUpload
	upserts int
	deletes int
}

func newFakePendingRepo() *fakePendingRepo {
	return &fakePendingRepo{rows: map[string]model.PendingLogUpload{}}
}

func (f *fakePendingRepo) UpsertPendingLogUpload(_ context.Context, rec model.PendingLogUpload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[rec.ExternalExecutionID] = rec
	f.upserts++
	return nil
}

func (f *fakePendingRepo) DeletePendingLogUpload(_ context.Context, externalExecutionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, externalExecutionID)
	f.deletes++
	return nil
}

func (f *fakePendingRepo) ListPendingLogUploads(_ context.Context) ([]model.PendingLogUpload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.PendingLogUpload, 0, len(f.rows))
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

func (r *fakeRunRepo) GetRunByExternalExecutionID(_ context.Context, id string) (*model.Run, error) {
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
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())

	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
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

	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	rows, err := repo.ListPendingLogUploads(context.Background())
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

	if err := u.RegisterDispatch(context.Background(), "exec-1", "", "key/x.log.gz"); err != nil {
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

	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	u.Forget(context.Background(), "exec-1")

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

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	runs := &fakeRunRepo{byExt: map[string]*model.Run{"exec-1": run}}
	u := NewLogUploader(repo, runs, logDir, fixedClock())
	u.httpClient = srv.Client()

	if err := u.RegisterDispatch(context.Background(), "exec-1", srv.URL, "key/exec-1.log.gz"); err != nil {
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

// Nil receiver and empty executionID are documented no-ops.
func TestArchiveNilReceiverIsNoop(t *testing.T) {
	var u *LogUploader
	result, err := u.Archive(context.Background(), "exec-1", "/tmp/x")
	if err != nil {
		t.Fatalf("Archive on nil receiver returned err: %v", err)
	}
	if result != nil {
		t.Errorf("Archive on nil receiver result = %+v, want nil", result)
	}
}

func TestArchiveEmptyExecutionIDIsNoop(t *testing.T) {
	u := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, t.TempDir(), fixedClock())
	result, err := u.Archive(context.Background(), "", "/tmp/x")
	if err != nil {
		t.Fatalf("Archive with empty id returned err: %v", err)
	}
	if result != nil {
		t.Errorf("Archive with empty id result = %+v, want nil", result)
	}
}

// Archive rejects an empty log file path once a pending entry exists for the
// execution — otherwise we'd PUT an empty body to S3.
func TestArchiveEmptyLogPathErrors(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())
	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}
	_, err := u.Archive(context.Background(), "exec-1", "")
	if err == nil {
		t.Fatal("expected error for empty log file path")
	}
}

// When the remote PUT fails with a permanent error, Archive surfaces the
// error without dropping the pending row (operator can retry on next boot).
func TestArchiveUploadFailureKeepsRow(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN", "exec-archive-err")
	logPath := writeRunLog(t, logDir, run, "hello\n")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // PermanentError → no retries
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, logDir, fixedClock())
	u.httpClient = srv.Client()

	if err := u.RegisterDispatch(context.Background(), "exec-archive-err", srv.URL, "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	_, err := u.Archive(context.Background(), "exec-archive-err", logPath)
	if err == nil {
		t.Fatal("expected upload failure to bubble up")
	}
	if got := repo.count(); got != 1 {
		t.Errorf("repo rows = %d, want 1 (failed upload must keep the row for retry)", got)
	}
}

func TestArchiveMissingLogFileForgetsEntry(t *testing.T) {
	logDir := t.TempDir()
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, logDir, fixedClock())

	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
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
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-gone",
		UploadURL:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{}}, t.TempDir(), fixedClock())

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
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-running",
		UploadURL:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-running": runningRun}}, logDir, fixedClock())

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

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-1",
		UploadURL:           srv.URL,
		LogPath:             "key/exec-1.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-1": run}}, logDir, fixedClock())
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

// fakeRunRepoError fakes a transient RunRepo failure so recoverOrphanRecord
// exercises the "lookup failed but row not dropped" branch.
type fakeRunRepoError struct {
	err error
}

func (f *fakeRunRepoError) GetRunByExternalExecutionID(_ context.Context, _ string) (*model.Run, error) {
	return nil, f.err
}

func TestRecoverOrphansKeepsRowOnTransientLookupError(t *testing.T) {
	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-flaky",
		UploadURL:           "https://upload/x",
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	transient := &fakeRunRepoError{err: context.DeadlineExceeded}
	u := NewLogUploader(repo, transient, t.TempDir(), fixedClock())

	var emitted int
	u.RecoverOrphans(context.Background(), func(string, LogUploaderResult) {
		emitted++
	})

	assert.Equal(t, 0, emitted, "no emit when lookup fails transiently")
	assert.Equal(t, 1, repo.count(), "transient errors must keep the row for the next boot")
}

// --- RegisterDispatch: edge cases ---

func TestRegisterDispatchNilUploaderIsNoop(t *testing.T) {
	var u *LogUploader
	if err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch on nil receiver returned err: %v", err)
	}
}

func TestRegisterDispatchEmptyExecutionIDIsNoop(t *testing.T) {
	repo := newFakePendingRepo()
	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())
	if err := u.RegisterDispatch(context.Background(), "", "https://upload/x", "key/x.log.gz"); err != nil {
		t.Fatalf("RegisterDispatch with empty id returned err: %v", err)
	}
	if got := repo.count(); got != 0 {
		t.Errorf("repo rows = %d, want 0", got)
	}
}

// failingPendingRepo lets us hit RegisterDispatch's persistence-error branch.
// Implements only the methods RegisterDispatch needs.
type failingPendingRepo struct {
	upsertErr error
}

func (f *failingPendingRepo) UpsertPendingLogUpload(_ context.Context, _ model.PendingLogUpload) error {
	return f.upsertErr
}

func (f *failingPendingRepo) DeletePendingLogUpload(_ context.Context, _ string) error { return nil }

func (f *failingPendingRepo) ListPendingLogUploads(_ context.Context) ([]model.PendingLogUpload, error) {
	return nil, nil
}

// failingDeleteRepo lets us hit forget's "delete returns error" branch.
type failingDeleteRepo struct {
	failingPendingRepo
}

func (f *failingDeleteRepo) DeletePendingLogUpload(_ context.Context, _ string) error {
	return errors.New("delete failed")
}

// listErrorPendingRepo lets us hit RecoverOrphans' list-error branch.
type listErrorPendingRepo struct {
	failingPendingRepo
}

func (l *listErrorPendingRepo) ListPendingLogUploads(_ context.Context) ([]model.PendingLogUpload, error) {
	return nil, errors.New("list failed")
}

func TestRecoverOrphansListErrorReturnsCleanly(t *testing.T) {
	u := NewLogUploader(&listErrorPendingRepo{}, &fakeRunRepo{}, t.TempDir(), fixedClock())
	// Must not panic; logs and returns.
	u.RecoverOrphans(context.Background(), func(string, LogUploaderResult) {
		t.Fatal("emit must not be called when listing fails")
	})
}

// Orphan rows for runs that haven't terminated yet are skipped without
// dropping the row — the next boot or terminal event handles them.
func TestRecoverOrphansArchiveErrorKeepsRow(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN", "exec-archive-fail")
	writeRunLog(t, logDir, run, "hello\n")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-archive-fail",
		UploadURL:           srv.URL,
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})

	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-archive-fail": run}}, logDir, fixedClock())
	u.httpClient = srv.Client()

	emitCalls := 0
	u.RecoverOrphans(context.Background(), func(string, LogUploaderResult) { emitCalls++ })

	assert.Equal(t, 0, emitCalls, "no emit when archival fails")
	assert.Equal(t, 1, repo.count(), "failed archival must keep the row for the next attempt")
}

// RecoverOrphans tolerates a nil emit callback (operator wants the recovery to
// happen but doesn't care about the resulting terminal updates).
func TestRecoverOrphansNilEmitIsSafe(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN", "exec-niloemit")
	writeRunLog(t, logDir, run, "hi\n")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	_ = repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExternalExecutionID: "exec-niloemit",
		UploadURL:           srv.URL,
		LogPath:             "key/x.log.gz",
		InsertedAt:          time.Now().Unix(),
	})
	u := NewLogUploader(repo, &fakeRunRepo{byExt: map[string]*model.Run{"exec-niloemit": run}}, logDir, fixedClock())
	u.httpClient = srv.Client()

	u.RecoverOrphans(context.Background(), nil)
	assert.Equal(t, 0, repo.count(), "successful recovery must still drop the row even with nil emit")
}

func TestForgetTolerantOfRepoDeleteError(t *testing.T) {
	u := NewLogUploader(&failingDeleteRepo{}, &fakeRunRepo{}, t.TempDir(), fixedClock())
	// Must not panic; the error is logged and swallowed.
	u.Forget(context.Background(), "exec-1")
}

func TestRegisterDispatchPropagatesUpsertError(t *testing.T) {
	want := errors.New("simulated upsert failure")
	repo := &failingPendingRepo{upsertErr: want}

	u := NewLogUploader(repo, &fakeRunRepo{}, t.TempDir(), fixedClock())
	err := u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key/x.log.gz")
	if !errors.Is(err, want) {
		t.Fatalf("RegisterDispatch err = %v, want %v", err, want)
	}
	// Persistence error must keep the in-memory entry out so the next boot's
	// recovery path is the single source of truth.
	if _, ok := u.lookup("exec-1"); ok {
		t.Error("in-memory entry must not be cached when persistence fails")
	}
}

func strPtr(s string) *string { return &s }
