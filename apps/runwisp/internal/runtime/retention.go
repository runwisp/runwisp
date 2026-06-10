// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	tasks        map[string]*model.Task
	interval     time.Duration
	cancel       context.CancelFunc
	logDir       string
	maxTotalSize int64
}

// NewRetentionCleaner builds a cleaner with the given cadence.
func NewRetentionCleaner(db storage.RunRepository, tasks map[string]*model.Task, interval time.Duration, logDir string, maxTotalSize int64) *RetentionCleaner {
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
	go cleaner.run(ctx)
}

func (cleaner *RetentionCleaner) Stop() {
	cleaner.cancel()
}

func (cleaner *RetentionCleaner) run(ctx context.Context) {
	ticker := time.NewTicker(cleaner.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cleaner.cleanOldRuns(ctx)
		case <-ctx.Done():
			slog.Debug("Stopping retention cleaner")
			return
		}
	}
}

func (cleaner *RetentionCleaner) cleanOldRuns(ctx context.Context) {
	slog.Debug("Running retention cleanup")

	totalDeleted := 0
	for _, task := range cleaner.tasks {
		// KeepRuns: 0 = inherited "no cap"; -1 = explicit unlimited; >0 = cap.
		// KeepFor:  0 = inherited "no cap"; >0 = cap. Either positive enables retention.
		if task.KeepFor <= 0 && task.KeepRuns <= 0 {
			continue
		}

		deletedRuns, err := cleaner.db.DeleteOldRuns(ctx, task)
		if err != nil {
			slog.Error("Failed to clean runs", "task", task.Name, "err", err)
			continue
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
	}

	if totalDeleted > 0 {
		slog.Info("Retention cleanup complete", "deleted", totalDeleted)
	}

	cleaner.enforceMaxTotalSize(ctx)

	// Return freed pages to the OS so RSS doesn't stay at the high-water mark
	// after a large cleanup batch.
	rdebug.FreeOSMemory()
}

// enforceMaxTotalSize deletes the oldest completed runs when the log directory
// exceeds the configured storage cap.
func (cleaner *RetentionCleaner) enforceMaxTotalSize(ctx context.Context) {
	if cleaner.maxTotalSize <= 0 || cleaner.logDir == "" {
		return
	}

	totalSize := dirSize(cleaner.logDir)
	if totalSize <= cleaner.maxTotalSize {
		return
	}

	slog.Warn("Log storage exceeds storage.max_size, purging oldest runs",
		"current", config.FormatByteSize(totalSize), "limit", config.FormatByteSize(cleaner.maxTotalSize))

	// Fetch oldest completed runs in batches and delete until under limit
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
		if info, statErr := os.Stat(logPath); statErr == nil {
			*totalSize -= info.Size()
		}
		if info, statErr := os.Stat(logPath + ".idx"); statErr == nil {
			*totalSize -= info.Size()
		}
		logutil.RemoveLogFiles(logPath)
		logutil.RemoveEmptyParents(logPath, cleaner.logDir)
		if err := cleaner.db.DeleteRun(ctx, run.ID); err != nil {
			slog.Warn("Failed to delete run during size enforcement", "id", run.ID, "err", err)
			continue
		}
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
