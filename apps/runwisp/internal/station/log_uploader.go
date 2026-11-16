// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/logarchive"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// archiveTimeout caps one archive: asking for the target plus gzip and upload.
// Daemon logs are typically a few MB, so even a slow link finishes well under
// it; it keeps a misbehaving upload endpoint from wedging finalization.
const archiveTimeout = 90 * time.Second

// uploadRequester asks the station for upload targets (upload:request).
type uploadRequester func(ctx context.Context, req protocol.UploadRequest) (protocol.UploadRequestResult, error)

// LogUploader archives the logs of station executions to the targets the
// station hands out. Every dispatched execution is recorded in
// pending_log_uploads before its run starts and stays there until its archive
// is uploaded or the station declines it, so a crash or a dropped connection
// only delays the upload.
type LogUploader struct {
	repo       storage.PendingLogUploadRepository
	runRepo    ExternalRunGetter
	logDir     string
	request    uploadRequester
	httpClient *http.Client
	insecure   bool
	trusted    string
	now        func() time.Time

	mu sync.Mutex
	// pending maps each execution awaiting its archive to whether its
	// dispatch asked for logStream. The flag isn't persisted: a run a daemon
	// restart interrupted has nothing left to stream.
	pending   map[string]bool
	archiving map[string]bool // archives in flight, so two never race
	streams   map[string]*chunkStream
}

// NewLogUploader returns an uploader. logDir must match the executor's LogDir.
// now stamps the persisted records; request asks the station for targets.
func NewLogUploader(repo storage.PendingLogUploadRepository, runRepo ExternalRunGetter, logDir string, now func() time.Time, request uploadRequester) *LogUploader {
	return &LogUploader{
		repo:       repo,
		runRepo:    runRepo,
		logDir:     logDir,
		request:    request,
		httpClient: logarchive.SafeClient(false, ""),
		now:        func() time.Time { return now().UTC() },
		pending:    map[string]bool{},
		archiving:  map[string]bool{},
		streams:    map[string]*chunkStream{},
	}
}

// AllowInsecure lifts the https + public-IP upload guard
// (RUNWISP_STATION_ALLOW_INSECURE). Call before the first upload.
func (u *LogUploader) AllowInsecure() {
	u.insecure = true
	u.httpClient = logarchive.SafeClient(true, u.trusted)
}

// TrustHost exempts the station's own "host:port" (logarchive.HostPort) from
// the public-IP upload guard. Call before the first upload.
func (u *LogUploader) TrustHost(hostPort string) {
	u.trusted = hostPort
	u.httpClient = logarchive.SafeClient(u.insecure, hostPort)
}

// RegisterDispatch records that executionID's log is to be archived. stream
// asks for chunk uploads while the run goes (see StartStream).
func (u *LogUploader) RegisterDispatch(ctx context.Context, executionID string, stream bool) error {
	u.mu.Lock()
	u.pending[executionID] = stream
	u.mu.Unlock()
	if u.repo == nil {
		return nil
	}
	return u.repo.UpsertPendingLogUpload(ctx, model.PendingLogUpload{ExecutionID: executionID, InsertedAt: u.now().Unix()})
}

// LoadPending reads the recorded executions at boot and returns their ids.
func (u *LogUploader) LoadPending(ctx context.Context) []string {
	if u.repo == nil {
		return nil
	}
	recs, err := u.repo.ListPendingLogUploads(ctx)
	if err != nil {
		slog.Warn("failed to list pending log uploads", "err", err)
		return nil
	}
	ids := make([]string, 0, len(recs))
	u.mu.Lock()
	for _, rec := range recs {
		if _, ok := u.pending[rec.ExecutionID]; !ok {
			u.pending[rec.ExecutionID] = false
		}
		ids = append(ids, rec.ExecutionID)
	}
	u.mu.Unlock()
	return ids
}

// Archive uploads the finished run's log and returns what the second finished
// update reports, or nil when there is nothing to report: the execution has no
// pending archive, its log is gone, the station declined it, or the upload
// failed and stays pending for RetryPending. chunks is how many log chunks
// were uploaded, nil when unknown.
func (u *LogUploader) Archive(ctx context.Context, run *model.Run, chunks *int) *protocol.LogArchive {
	if run == nil {
		return nil
	}
	executionID := executionIDOf(run)
	if !u.begin(executionID) {
		return nil
	}
	defer u.end(executionID)

	logFilePath := logutil.ResolveRunLogPath(u.logDir, run.TaskName, run.ID, run.CreatedAt)
	if _, err := os.Stat(logFilePath); errors.Is(err, os.ErrNotExist) {
		u.forget(ctx, executionID)
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	res, err := u.request(ctx, protocol.UploadRequest{ExecutionID: executionID, Purpose: protocol.UploadPurposeLogArchive})
	if err != nil {
		if se, ok := errors.AsType[*StationError](err); ok && !se.Retryable {
			slog.Warn("station refused the log archive; keeping the log here", "executionId", executionID, "err", err)
			u.forget(ctx, executionID)
			return nil
		}
		slog.Info("log archive postponed until the station is reachable", "executionId", executionID, "err", err)
		return nil
	}
	if len(res.Uploads) == 0 {
		// The station doesn't want this archive (log storage off or full).
		u.forget(ctx, executionID)
		return nil
	}
	target := res.Uploads[0]
	size, err := logarchive.Archive(ctx, u.httpClient, logarchive.Target{URL: target.URL, Method: target.Method, Headers: target.Headers}, logFilePath, u.insecure)
	if err != nil {
		slog.Warn("log archive upload failed; retrying after the next reconnect", "executionId", executionID, "err", err)
		return nil
	}

	u.forget(ctx, executionID)
	if err := os.Remove(logFilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("failed to remove archived log file", "executionId", executionID, "path", logFilePath, "err", err)
	}
	// The meta sidecar is meaningless once the log is gone.
	_ = os.Remove(logutil.MetaPath(logFilePath))
	return &protocol.LogArchive{Key: target.Key, Bytes: size, Chunks: chunks}
}

// RetryPending uploads the archives still pending for finished runs, after a
// welcome; live reports the executions still accepted or running, whose runs
// may not even be stored yet. emit stores each run's finished update with its
// archive.
func (u *LogUploader) RetryPending(ctx context.Context, live func(executionID string) bool, emit func(*protocol.ExecutionUpdate)) {
	u.mu.Lock()
	ids := make([]string, 0, len(u.pending))
	for id := range u.pending {
		if !u.archiving[id] && u.streams[id] == nil && !live(id) {
			ids = append(ids, id)
		}
	}
	u.mu.Unlock()
	slices.Sort(ids)

	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		u.retry(ctx, id, emit)
	}
}

func (u *LogUploader) retry(ctx context.Context, id string, emit func(*protocol.ExecutionUpdate)) {
	// Every pending id is the daemon's own: a dispatch's, or a service run's.
	run, err := findExecutionRun(ctx, u.runRepo, id, func(*model.Run) bool { return true })
	if errors.Is(err, storage.ErrNotFound) {
		slog.Info("dropping pending log archive: run not found", "executionId", id)
		u.forget(ctx, id)
		return
	}
	if err != nil || !run.Status.IsTerminal() {
		return
	}
	if archived := u.Archive(ctx, run, nil); archived != nil {
		if update := executionUpdate(run); update != nil {
			update.Log = archived
			emit(update)
		}
	}
}

func (u *LogUploader) begin(executionID string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if _, ok := u.pending[executionID]; !ok || u.archiving[executionID] {
		return false
	}
	u.archiving[executionID] = true
	return true
}

func (u *LogUploader) end(executionID string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.archiving, executionID)
}

// isPending reports whether executionID's log still waits to be archived.
func (u *LogUploader) isPending(executionID string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, ok := u.pending[executionID]
	return ok
}

func (u *LogUploader) wantsStream(executionID string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.pending[executionID]
}

// forget drops an execution's pending archive, in memory and on disk.
func (u *LogUploader) forget(ctx context.Context, executionID string) {
	u.mu.Lock()
	delete(u.pending, executionID)
	u.mu.Unlock()
	if u.repo != nil {
		if err := u.repo.DeletePendingLogUpload(context.WithoutCancel(ctx), executionID); err != nil {
			slog.Warn("failed to delete pending log upload row", "executionId", executionID, "err", err)
		}
	}
}
