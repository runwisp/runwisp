// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	rdebug "runtime/debug"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// RetentionCleaner periodically prunes old runs and their logs.
type RetentionCleaner struct {
	db           storage.RunRepository
	tasks        *TaskRegistry
	interval     time.Duration
	cancel       context.CancelFunc
	logDir       string
	maxTotalSize atomic.Int64 // swapped by SetMaxTotalSize on a config reload
	eventBus     *events.Bus
}

// NewRetentionCleaner builds a cleaner with the given cadence. tasks is the
// live registry: the cleaner's background ticker ranges it under the read lock,
// so a reload that adds or removes tasks is picked up on the next pass.
// eventBus may be nil (tests that don't care about the resulting SSE fan-out);
// a nil bus just means no run.deleted event is published.
func NewRetentionCleaner(db storage.RunRepository, tasks *TaskRegistry, interval time.Duration, logDir string, maxTotalSize int64, eventBus *events.Bus) *RetentionCleaner {
	if interval == 0 {
		interval = time.Hour
	}

	cleaner := &RetentionCleaner{
		db:       db,
		tasks:    tasks,
		interval: interval,
		logDir:   logDir,
		eventBus: eventBus,
	}
	cleaner.maxTotalSize.Store(maxTotalSize)
	return cleaner
}

// SetMaxTotalSize changes [storage] max_size; the next pass enforces it.
func (cleaner *RetentionCleaner) SetMaxTotalSize(n int64) {
	cleaner.maxTotalSize.Store(n)
}

// publishDeleted emits run.deleted for a run this cleaner just removed, so
// clients tracking a live total-runs count (the dashboard) stay in sync
// instead of only learning about it on their next full refetch. Mirrors
// runService.publishDeleted in internal/server.
func (cleaner *RetentionCleaner) publishDeleted(runID, taskName string) {
	cleaner.eventBus.Publish(events.EventRunDeleted, events.RunDeletedEvent{
		RunID:    runID,
		TaskName: taskName,
	})
}

// deleteAndPublish removes the given run rows and, only once that succeeds,
// fans out run.deleted for each.
func (cleaner *RetentionCleaner) deleteAndPublish(ctx context.Context, ids []string, refs []storage.RunRef) {
	if err := cleaner.db.DeleteRunsByIDs(ctx, ids); err != nil {
		slog.Error("Failed to delete old run rows", "count", len(ids), "err", err)
		return
	}
	for _, ref := range refs {
		cleaner.publishDeleted(ref.ID, ref.TaskName)
	}
}

func (cleaner *RetentionCleaner) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	cleaner.cancel = cancel
	// Run the first pass synchronously so a cleanup is observably complete by
	// the time Start returns; the goroutine then only services the ticker.
	// Tests can assert on the initial pass without sleeping for the goroutine.
	cleaner.cleanOldRuns(ctx)
	startTicker(ctx, cleaner.interval, "Stopping retention cleaner", cleaner.cleanOldRuns)
}

func (cleaner *RetentionCleaner) Stop() {
	cleaner.cancel()
}

func (cleaner *RetentionCleaner) cleanOldRuns(ctx context.Context) {
	slog.Debug("Running retention cleanup")

	totalDeleted := 0
	var allIDs []string
	var deletedRefs []storage.RunRef
	cleaner.tasks.Range(func(_ string, task *model.Task) bool {
		// KeepRuns: nil = no cap; 0 = keep no completed runs; >0 = cap.
		// KeepFor:  0 = no cap; >0 = cap. Either a set KeepRuns or a positive
		// KeepFor enables retention.
		if task.KeepFor <= 0 && task.KeepRuns == nil {
			return true
		}

		oldRuns, err := cleaner.db.SelectOldRuns(ctx, task)
		if err != nil {
			slog.Error("Failed to select old runs", "task", task.Name, "err", err)
			return true
		}

		// Log files come off disk before the row is deleted (batched below): a
		// crash in between leaves a harmless log-less row rather than an
		// unreclaimable orphan log file — same policy as deleteRunBatch.
		for _, run := range oldRuns {
			logutil.RemoveRunLogs(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)
			allIDs = append(allIDs, run.ID)
			deletedRefs = append(deletedRefs, storage.RunRef{ID: run.ID, TaskName: run.TaskName})
		}

		if len(oldRuns) > 0 {
			slog.Info("Retention cleaned runs", "count", len(oldRuns), "task", task.Name)
			totalDeleted += len(oldRuns)
		}
		return true
	})

	if len(allIDs) > 0 {
		cleaner.deleteAndPublish(ctx, allIDs, deletedRefs)
	}

	if totalDeleted > 0 {
		slog.Info("Retention cleanup complete", "deleted", totalDeleted)
	}

	sizeDeleted := cleaner.enforceMaxTotalSize(ctx)

	// Return freed pages to the OS only after an actual cleanup, so RSS drops
	// from the high-water mark a large batch left behind. Routine idle reclaim
	// lives in runtime.MemoryReclaimer — no point forcing a full GC on every
	// empty pass.
	if totalDeleted > 0 || sizeDeleted > 0 {
		rdebug.FreeOSMemory()
	}
}

// enforceMaxTotalSize deletes the oldest completed runs when the log directory
// exceeds the configured storage cap. Returns the number of runs deleted.
//
// Only completed runs' files can be reclaimed. When the rest (active runs'
// logs, files no run owns) already exceeds the cap, deleting every completed
// run still would not get under it, so it deletes nothing and warns instead of
// wiping history for no gain.
func (cleaner *RetentionCleaner) enforceMaxTotalSize(ctx context.Context) int {
	limit := cleaner.maxTotalSize.Load()
	if limit <= 0 || cleaner.logDir == "" {
		return 0
	}

	totalSize := dirSize(cleaner.logDir)
	if totalSize <= limit {
		return 0
	}

	deletable, deletableBytes, err := cleaner.deletableRuns(ctx)
	if err != nil {
		slog.Warn("Could not list runs to enforce storage.max_size", "err", err)
		return 0
	}
	if totalSize-deletableBytes > limit {
		slog.Warn("Log storage exceeds storage.max_size, but completed runs are not the cause; keeping them. "+
			"Active runs' logs and files not attached to any run alone are over the limit: raise storage.max_size or remove the stray files",
			"current", config.FormatByteSize(totalSize), "limit", config.FormatByteSize(limit),
			"completed_runs", config.FormatByteSize(deletableBytes), "other", config.FormatByteSize(totalSize-deletableBytes))
		return 0
	}

	slog.Warn("Log storage exceeds storage.max_size, purging oldest runs",
		"current", config.FormatByteSize(totalSize), "limit", config.FormatByteSize(limit))

	deleted := cleaner.deleteRunBatch(ctx, deletable, &totalSize, limit)
	if deleted > 0 {
		slog.Info("Purged runs to enforce storage.max_size", "deleted", deleted)
	}
	return deleted
}

// deletableRuns lists the completed runs that still have log files on disk,
// oldest first, and the bytes deleting all of them would free. Only the fields
// deleteRunBatch reads are kept, so a long history stays cheap to hold.
func (cleaner *RetentionCleaner) deletableRuns(ctx context.Context) ([]model.Run, int64, error) {
	const pageSize = 100
	var (
		runs  []model.Run
		bytes int64
	)
	for offset := 0; ; offset += pageSize {
		page, err := cleaner.db.QueryRuns(ctx, storage.RunQuery{
			Limit:         pageSize,
			Offset:        offset,
			SortField:     storage.SortColumnCreatedAt,
			SortDirection: storage.SortAsc,
		})
		if err != nil {
			return nil, 0, err
		}
		for _, run := range page {
			if !run.Status.IsTerminal() {
				continue
			}
			if size := cleaner.runLogBytes(run); size > 0 {
				runs = append(runs, model.Run{ID: run.ID, TaskName: run.TaskName, CreatedAt: run.CreatedAt, Status: run.Status})
				bytes += size
			}
		}
		if len(page) < pageSize {
			return runs, bytes, nil
		}
	}
}

// runLogBytes is the size of a run's log, its meta file and the rotated-away
// .prev segment: everything RemoveRunLogs deletes, and dirSize counted.
func (cleaner *RetentionCleaner) runLogBytes(run model.Run) int64 {
	logPath := logutil.ResolveRunLogPath(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)
	var total int64
	for _, p := range []string{logPath, logutil.MetaPath(logPath), logutil.PrevPath(logPath)} {
		if info, err := os.Stat(p); err == nil {
			total += info.Size()
		}
	}
	return total
}

// deleteRunBatch deletes terminal runs from runs until totalSize drops to or
// below limit. Updates *totalSize in place. Returns the number deleted.
func (cleaner *RetentionCleaner) deleteRunBatch(ctx context.Context, runs []model.Run, totalSize *int64, limit int64) int {
	deleted := 0
	for _, run := range runs {
		if !run.Status.IsTerminal() {
			continue
		}
		*totalSize -= cleaner.runLogBytes(run)
		logutil.RemoveRunLogs(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)
		if err := cleaner.db.DeleteRun(ctx, run.ID); err != nil {
			slog.Warn("Failed to delete run during size enforcement", "id", run.ID, "err", err)
			continue
		}
		cleaner.publishDeleted(run.ID, run.TaskName)
		deleted++
		if *totalSize <= limit {
			break
		}
	}
	return deleted
}

// dirSize returns the total size in bytes of all files under dir (recursive).
func dirSize(dir string) int64 {
	var total int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
