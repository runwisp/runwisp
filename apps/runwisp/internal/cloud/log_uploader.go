// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/cloud/logarchive"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/storage"
)

// archiveTimeout caps a single terminal archive operation. Daemon log files
// in the field are typically a few MB; even on a slow link, gzip + PUT
// finishes in well under a minute. Keep this conservative so the eventbridge
// goroutine never wedges on a misbehaving S3 endpoint.
const archiveTimeout = 90 * time.Second

// LogUploaderResult is the artefact of a successful upload — what the daemon
// echoes back to cloud on the terminal `execution:update`.
type LogUploaderResult struct {
	LogPath string
	LogSize int64
}

// LogUploader coordinates dispatch metadata + on-terminal archival to the
// signed PUT URL the cloud supplied. Crash recovery is handled via the
// `pending_log_uploads` SQLite table written at dispatch time and removed
// only on successful upload.
type LogUploader struct {
	repo         storage.PendingLogUploadRepository
	runRepo      storage.RunRepository
	logDir       string
	httpClient   *http.Client
	now          func() time.Time
	deleteOnDone bool

	mu      sync.Mutex
	pending map[string]uploadEntry
}

type uploadEntry struct {
	uploadURL string
	logPath   string
}

// NewLogUploader returns a coordinator. logDir must match the executor's
// LogDir so file paths can be resolved on terminal.
func NewLogUploader(repo storage.PendingLogUploadRepository, runRepo storage.RunRepository, logDir string) *LogUploader {
	return &LogUploader{
		repo:         repo,
		runRepo:      runRepo,
		logDir:       logDir,
		httpClient:   http.DefaultClient,
		now:          func() time.Time { return time.Now().UTC() },
		deleteOnDone: true,
		pending:      make(map[string]uploadEntry),
	}
}

// RegisterDispatch persists the upload coordinates the cloud handed us. An
// empty uploadURL means "skip archival for this dispatch" — we drop the
// entry on the floor and return without error.
func (u *LogUploader) RegisterDispatch(executionID, uploadURL, logPath string) error {
	if u == nil || executionID == "" {
		return nil
	}
	if uploadURL == "" {
		return nil
	}
	rec := storage.PendingLogUpload{
		ExternalExecutionID: executionID,
		UploadURL:           uploadURL,
		LogPath:             logPath,
		InsertedAt:          u.now().Unix(),
	}
	if u.repo != nil {
		if err := u.repo.UpsertPendingLogUpload(rec); err != nil {
			slog.Warn("failed to persist pending log upload", "executionId", executionID, "err", err)
			return err
		}
	}
	u.mu.Lock()
	u.pending[executionID] = uploadEntry{uploadURL: uploadURL, logPath: logPath}
	u.mu.Unlock()
	return nil
}

// Archive performs a single gzip + PUT for the terminal log of executionID.
// Returns nil, nil when there is no pending upload for the execution (e.g.
// the dispatch came with an empty logUploadUrl). On success, the local log
// file and the persistence row are both removed.
func (u *LogUploader) Archive(ctx context.Context, executionID, logFilePath string) (*LogUploaderResult, error) {
	if u == nil || executionID == "" {
		return nil, nil
	}
	entry, ok := u.lookup(executionID)
	if !ok {
		return nil, nil
	}
	if logFilePath == "" {
		return nil, errors.New("log_uploader: empty log file path")
	}
	if _, err := os.Stat(logFilePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			u.forget(executionID)
			return nil, nil
		}
		return nil, err
	}

	archiveCtx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	size, err := logarchive.Archive(archiveCtx, u.httpClient, entry.uploadURL, logFilePath)
	if err != nil {
		return nil, err
	}

	u.forget(executionID)
	if u.deleteOnDone {
		if removeErr := os.Remove(logFilePath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			slog.Warn("failed to remove archived log file", "executionId", executionID, "path", logFilePath, "err", removeErr)
		}
		// Drop the meta sidecar too — it is meaningless once the log is gone.
		_ = os.Remove(logutil.MetaPath(logFilePath))
	}
	return &LogUploaderResult{LogPath: entry.logPath, LogSize: size}, nil
}

// RecoverOrphans is called at startup. For every persisted dispatch, find
// the matching run; if the run has terminated, attempt the upload again.
// Successful uploads emit the terminal update through emit so the cloud
// learns the logPath/logSize it would have received originally.
func (u *LogUploader) RecoverOrphans(ctx context.Context, emit func(executionID string, result LogUploaderResult)) {
	if u == nil || u.repo == nil {
		return
	}
	recs, err := u.repo.ListPendingLogUploads()
	if err != nil {
		slog.Warn("failed to list pending log uploads", "err", err)
		return
	}
	for _, rec := range recs {
		u.mu.Lock()
		u.pending[rec.ExternalExecutionID] = uploadEntry{uploadURL: rec.UploadURL, logPath: rec.LogPath}
		u.mu.Unlock()

		run, runErr := u.runRepo.GetRunByExternalExecutionID(rec.ExternalExecutionID)
		if runErr != nil {
			if errors.Is(runErr, storage.ErrNotFound) {
				slog.Info("dropping orphan log upload row: run not found", "executionId", rec.ExternalExecutionID)
				u.forget(rec.ExternalExecutionID)
				continue
			}
			slog.Warn("failed to query run for orphan upload", "executionId", rec.ExternalExecutionID, "err", runErr)
			continue
		}
		if !run.Status.IsTerminal() {
			slog.Info("skipping orphan upload: run not terminal yet", "executionId", rec.ExternalExecutionID)
			continue
		}

		logFilePath := logutil.ResolveRunLogPath(u.logDir, run.TaskName, run.ID, run.CreatedAt)
		result, err := u.Archive(ctx, rec.ExternalExecutionID, logFilePath)
		if err != nil {
			slog.Warn("orphan log upload failed", "executionId", rec.ExternalExecutionID, "err", err)
			continue
		}
		if result == nil {
			continue
		}
		if emit != nil {
			emit(rec.ExternalExecutionID, *result)
		}
	}
}

// Forget drops in-memory state for an execution without touching storage.
// Used when the dispatch is invalid or a run terminates with no upload URL.
func (u *LogUploader) Forget(executionID string) {
	u.forget(executionID)
}

func (u *LogUploader) lookup(executionID string) (uploadEntry, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	e, ok := u.pending[executionID]
	return e, ok
}

func (u *LogUploader) forget(executionID string) {
	u.mu.Lock()
	delete(u.pending, executionID)
	u.mu.Unlock()
	if u.repo != nil {
		if err := u.repo.DeletePendingLogUpload(executionID); err != nil {
			slog.Warn("failed to delete pending log upload row", "executionId", executionID, "err", err)
		}
	}
}
