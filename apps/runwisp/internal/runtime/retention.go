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

// deleteClaimedRuns finishes durable cleanup claims. Files come off disk
// first so a failed delete or a crash leaves a claim the next sweep can retry.
func deleteClaimedRuns(ctx context.Context, db storage.RunRepository, logDir string, refs []storage.RunRef) []storage.RunRef {
	var ids []string
	var removed []storage.RunRef
	for _, ref := range refs {
		if ctx.Err() != nil {
			break
		}
		if err := logutil.RemoveRunLogs(logDir, ref.TaskName, ref.ID, ref.CreatedAt); err != nil {
			slog.Warn("Failed to remove run logs", "id", ref.ID, "err", err)
			continue
		}
		ids = append(ids, ref.ID)
		removed = append(removed, ref)
	}
	if len(ids) == 0 {
		return nil
	}
	if err := db.DeleteRunsByIDs(ctx, ids); err != nil {
		slog.Error("Failed to delete run rows", "count", len(ids), "err", err)
		return nil
	}
	return removed
}

func (cleaner *RetentionCleaner) deleteAndPublish(ctx context.Context, refs []storage.RunRef) []storage.RunRef {
	deleted := deleteClaimedRuns(ctx, cleaner.db, cleaner.logDir, refs)
	if cleaner.eventBus != nil {
		for _, ref := range deleted {
			cleaner.publishDeleted(ref.ID, ref.TaskName)
		}
	}
	return deleted
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

	pending, err := cleaner.db.ListPendingRetentionDeletes(ctx)
	if err != nil {
		slog.Warn("Could not list pending retention deletes", "err", err)
		return
	}
	allIDs := cleaner.selectRetentionCandidates(ctx)

	var claimed []storage.RunRef
	if len(allIDs) > 0 {
		claimed, err = cleaner.db.ClaimRetentionDeletes(ctx, allIDs)
		if err != nil {
			slog.Error("Could not claim runs for retention", "err", err)
		}
	}
	deleted := cleaner.deleteAndPublish(ctx, append(pending, claimed...))
	counts := make(map[string]int)
	for _, ref := range deleted {
		counts[ref.TaskName]++
	}
	for task, count := range counts {
		slog.Info("Retention cleaned runs", "count", count, "task", task)
	}

	totalDeleted := len(deleted)
	if totalDeleted > 0 {
		slog.Info("Retention cleanup complete", "deleted", totalDeleted)
	}

	sizeDeleted := 0
	// Finish outstanding removals before choosing more runs for the size cap.
	if err == nil && totalDeleted == len(pending)+len(claimed) {
		sizeDeleted = cleaner.enforceMaxTotalSize(ctx)
	}

	// Return freed pages to the OS only after an actual cleanup, so RSS drops
	// from the high-water mark a large batch left behind. Routine idle reclaim
	// lives in runtime.MemoryReclaimer — no point forcing a full GC on every
	// empty pass.
	if totalDeleted > 0 || sizeDeleted > 0 {
		rdebug.FreeOSMemory()
	}
}

func (cleaner *RetentionCleaner) selectRetentionCandidates(ctx context.Context) []string {
	var allIDs []string
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

		for _, run := range oldRuns {
			allIDs = append(allIDs, run.ID)
		}
		return true
	})

	return allIDs
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

	// Take the shortest oldest-first prefix that gets under the cap.
	var ids []string
	for _, run := range deletable {
		if totalSize <= limit {
			break
		}
		ids = append(ids, run.id)
		totalSize -= run.bytes
	}
	claimed, err := cleaner.db.ClaimRetentionDeletes(ctx, ids)
	if err != nil {
		slog.Warn("Could not claim runs during size enforcement", "err", err)
		return 0
	}
	deleted := len(cleaner.deleteAndPublish(ctx, claimed))
	if deleted > 0 {
		slog.Info("Purged runs to enforce storage.max_size", "deleted", deleted)
	}
	return deleted
}

// sizedRun is a completed run's id and the bytes its log files take.
type sizedRun struct {
	id    string
	bytes int64
}

// deletableRuns lists the completed runs that still have log files on disk,
// oldest first, and the bytes deleting all of them would free.
func (cleaner *RetentionCleaner) deletableRuns(ctx context.Context) ([]sizedRun, int64, error) {
	const pageSize = 100
	var (
		runs  []sizedRun
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
				runs = append(runs, sizedRun{id: run.ID, bytes: size})
				bytes += size
			}
		}
		if len(page) < pageSize {
			return runs, bytes, nil
		}
	}
}

// runLogBytes is the size of a run's log files: everything RemoveRunLogs
// deletes, and dirSize counted.
func (cleaner *RetentionCleaner) runLogBytes(run model.Run) int64 {
	var total int64
	for _, p := range logutil.LogFiles(logutil.ResolveRunLogPath(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)) {
		if info, err := os.Lstat(p); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
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
