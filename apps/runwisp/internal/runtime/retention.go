// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	rdebug "runtime/debug"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// RetentionCleaner periodically prunes old runs and their logs.
type RetentionCleaner struct {
	db           storage.RunRepository
	tasks        *TaskRegistry
	interval     time.Duration
	cancel       context.CancelFunc
	logDir       string
	maxTotalSize int64
}

// NewRetentionCleaner builds a cleaner with the given cadence. tasks is the
// live registry: the cleaner's background ticker ranges it under the read lock,
// so a reload that adds or removes tasks is picked up on the next pass.
func NewRetentionCleaner(db storage.RunRepository, tasks *TaskRegistry, interval time.Duration, logDir string, maxTotalSize int64) *RetentionCleaner {
	if interval == 0 {
		interval = time.Hour
	}

	return &RetentionCleaner{
		db:           db,
		tasks:        tasks,
		interval:     interval,
		logDir:       logDir,
		maxTotalSize: maxTotalSize,
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
	cleaner.tasks.Range(func(_ string, task *model.Task) bool {
		// KeepRuns: nil = no cap; 0 = keep no completed runs; >0 = cap.
		// KeepFor:  0 = no cap; >0 = cap. Either a set KeepRuns or a positive
		// KeepFor enables retention.
		if task.KeepFor <= 0 && task.KeepRuns == nil {
			return true
		}

		deletedRuns, err := cleaner.db.DeleteOldRuns(ctx, task)
		if err != nil {
			slog.Error("Failed to clean runs", "task", task.Name, "err", err)
			return true
		}

		for _, run := range deletedRuns {
			logPath := logutil.ResolveRunLogPath(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)
			logutil.RemoveLogFiles(logPath)
			logutil.RemoveEmptyParents(logPath, cleaner.logDir)
		}

		if len(deletedRuns) > 0 {
			slog.Info("Retention cleaned runs", "count", len(deletedRuns), "task", task.Name)
			totalDeleted += len(deletedRuns)
		}
		return true
	})

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
func (cleaner *RetentionCleaner) enforceMaxTotalSize(ctx context.Context) int {
	if cleaner.maxTotalSize <= 0 || cleaner.logDir == "" {
		return 0
	}

	totalSize := dirSize(cleaner.logDir)
	if totalSize <= cleaner.maxTotalSize {
		return 0
	}

	slog.Warn("Log storage exceeds storage.max_size, purging oldest runs",
		"current", config.FormatByteSize(totalSize), "limit", config.FormatByteSize(cleaner.maxTotalSize))

	deleted := 0
	offset := 0
	for totalSize > cleaner.maxTotalSize {
		runs, err := cleaner.db.QueryRuns(ctx, storage.RunQuery{
			Limit:         100,
			Offset:        offset,
			SortField:     storage.SortColumnCreatedAt,
			SortDirection: storage.SortAsc,
		})
		if err != nil || len(runs) == 0 {
			break
		}

		n := cleaner.deleteRunBatch(ctx, runs, &totalSize)
		deleted += n

		// No terminal runs found in this batch — advance past them
		if n == 0 {
			offset += len(runs)
		}
	}

	if deleted > 0 {
		slog.Info("Purged runs to enforce storage.max_size", "deleted", deleted)
	}
	return deleted
}

// deleteRunBatch deletes terminal runs from runs until totalSize drops to or
// below maxTotalSize. Updates *totalSize in place. Returns the number deleted.
func (cleaner *RetentionCleaner) deleteRunBatch(ctx context.Context, runs []model.Run, totalSize *int64) int {
	deleted := 0
	for _, run := range runs {
		if !run.Status.IsTerminal() {
			continue
		}
		logPath := logutil.ResolveRunLogPath(cleaner.logDir, run.TaskName, run.ID, run.CreatedAt)
		var freed int64
		if info, statErr := os.Stat(logPath); statErr == nil {
			freed += info.Size()
		}
		if info, statErr := os.Stat(logutil.MetaPath(logPath)); statErr == nil {
			freed += info.Size()
		}
		// RemoveLogFiles also deletes the rotated-away .prev segment, and the
		// initial dirSize counted it — subtract it too or the tracked total
		// stays high and the loop over-deletes runs.
		if info, statErr := os.Stat(logutil.PrevPath(logPath)); statErr == nil {
			freed += info.Size()
		}
		// Delete the DB row before the log file (matching cleanOldRuns and
		// softdelete_purger.purge): a SIGKILL between the two steps leaves an
		// orphaned log file (harmless) rather than a DB row pointing at a
		// deleted log (a "ghost" the operator can click into and get an error).
		if err := cleaner.db.DeleteRun(ctx, run.ID); err != nil {
			slog.Warn("Failed to delete run during size enforcement", "id", run.ID, "err", err)
			continue
		}
		*totalSize -= freed
		logutil.RemoveLogFiles(logPath)
		logutil.RemoveEmptyParents(logPath, cleaner.logDir)
		deleted++
		if *totalSize <= cleaner.maxTotalSize {
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
