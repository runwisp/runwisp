// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // registers the SQLite driver for database/sql

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

const (
	ConfigKeyFingerprint = "fingerprint"

	maxSearchQueryLength = 100
	retentionBatchSize   = 1000
	sqliteBusyTimeout    = 5000
	sqliteMaxOpenConns   = 1
	// sqliteCacheSizeKiB pins the page cache. A negative cache_size is read by
	// SQLite as KiB rather than pages, so this caps the cache at ~2 MiB instead
	// of letting modernc's allocator drift up to (and hold) its high-water mark.
	// Plenty for a metadata-only store; log bodies live on disk, not in SQLite.
	sqliteCacheSizeKiB = -2000
	// sqliteSoftHeapLimitBytes caps the SQLite allocator's heap (16 MiB), forcing
	// it to spill caches rather than grow unbounded during big list/retention
	// scans. Bounds worst-case SQLite RSS at steady state.
	sqliteSoftHeapLimitBytes = 16 << 20
)

// RunRepository defines the interface for run persistence.
type RunRepository interface {
	CreateRun(ctx context.Context, run *model.Run) error
	UpdateRun(ctx context.Context, run *model.Run) error
	GetRun(ctx context.Context, id string) (*model.Run, error)
	GetRunByExecutionID(ctx context.Context, executionID string) (*model.Run, error)
	CountRunsFiltered(ctx context.Context, filter model.RunFilter) (int64, error)
	QueryRuns(ctx context.Context, q RunQuery) ([]model.Run, error)
	DeleteRun(ctx context.Context, id string) error
	SelectOldRuns(ctx context.Context, task *model.Task) ([]model.Run, error)
	DeleteRunsByIDs(ctx context.Context, ids []string) error
	MarkCrashedRuns(ctx context.Context) (int64, error)
	GetPendingRuns(ctx context.Context) ([]model.Run, error)
	GetLastRunByTask(ctx context.Context, taskName string) (*model.Run, error)
	GetRunSummary(ctx context.Context) (*model.RunSummary, error)
	EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error
	GetTaskRegistration(ctx context.Context, taskName string) (*model.TaskRegistration, error)
	// GetTaskBootID returns the boot a run_on_start = "boot" task last fired
	// in, or "" when it never has.
	GetTaskBootID(ctx context.Context, taskName string) (string, error)
	SetTaskBootID(ctx context.Context, taskName, bootID string) error
	SoftDeleteRuns(ctx context.Context, sel model.RunSelector, deletedAt time.Time) ([]RunRef, error)
	RestoreRuns(ctx context.Context, sel model.RunSelector) ([]model.Run, error)
	ResolveSelectorIDs(ctx context.Context, sel model.RunSelector, statusFilter string) ([]RunRef, error)
	SelectExpiredSoftDeletes(ctx context.Context, ttl time.Duration) ([]RunRef, error)
	Close() error
}

// ConfigRepository stores and retrieves named daemon configuration values.
type ConfigRepository interface {
	GetConfigValue(ctx context.Context, key string) (string, bool, error)
	SetConfigValue(ctx context.Context, key, value string) error
}

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(ctx context.Context, rec model.PendingLogUpload) error
	DeletePendingLogUpload(ctx context.Context, executionID string) error
	ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error)
}

// TaskPauseRepository persists operator pauses of a task's cron schedule so a
// pause survives a daemon restart (see runtime.Scheduler.Pause).
type TaskPauseRepository interface {
	PauseTaskSchedule(ctx context.Context, taskName string, at time.Time) error
	ResumeTaskSchedule(ctx context.Context, taskName string, at time.Time) error
	ListPausedTaskSchedules(ctx context.Context) (map[string]time.Time, error)
}

// Database is the full persistent store for the daemon: runs + configuration + notifications.
type Database interface {
	RunRepository
	ConfigRepository
	NotificationRepository
	PendingLogUploadRepository
	TaskPauseRepository
}

// SQLiteDatabase wraps persistence concerns for runs and configuration.
// Reads and writes route through the generated sqlcdb.Queries; the raw
// *sql.DB is retained only for lifecycle (Close) and maintenance pragmas.
type SQLiteDatabase struct {
	db *sql.DB
	q  *sqlcdb.Queries
}

// New opens the SQLite database and applies any pending forward-only
// migrations (see migrate.go).
func New(dbPath string) (Database, error) {
	// modernc.org/sqlite's default time.Time write format is Go's
	// time.Time.String() (e.g. "2026-08-22 14:39:12.06 +0200 CEST"): a
	// trailing zone-name abbreviation SQLite's own date/time functions
	// (julianday, strftime, ...) cannot parse, so they silently treat every
	// timestamp as NULL. _time_format=sqlite switches writes to a numeric-offset
	// format from the SQLite date/time spec (https://www.sqlite.org/lang_datefunc.html
	// "Time Strings" format 5), which those functions can parse. Read-side
	// parsing already tries this format regardless, so this only affects writes.
	db, err := sql.Open("sqlite", dbPath+"?_time_format=sqlite")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite uses single-writer serialized mode, limit to 1 connection.
	db.SetMaxOpenConns(sqliteMaxOpenConns)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=" + strconv.Itoa(sqliteBusyTimeout) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}
	// Bound SQLite's memory: pin the page cache, cap the allocator's heap, and
	// keep that bounded cache the only buffer (no growing mmap region). Lowers
	// idle RSS — see sqliteCacheSizeKiB / sqliteSoftHeapLimitBytes.
	if _, err := db.Exec("PRAGMA cache_size=" + strconv.Itoa(sqliteCacheSizeKiB) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set cache_size: %w", err)
	}
	if _, err := db.Exec("PRAGMA soft_heap_limit=" + strconv.Itoa(sqliteSoftHeapLimitBytes) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set soft_heap_limit: %w", err)
	}
	if _, err := db.Exec("PRAGMA mmap_size=0;"); err != nil {
		return nil, fmt.Errorf("failed to disable mmap: %w", err)
	}

	if err := runMigrations(db); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &SQLiteDatabase{db: db, q: sqlcdb.New(db)}, nil
}

func (db *SQLiteDatabase) CreateRun(ctx context.Context, run *model.Run) error {
	return db.q.CreateRun(ctx, runToCreateParams(run))
}

func (db *SQLiteDatabase) UpdateRun(ctx context.Context, run *model.Run) error {
	rows, err := db.q.UpdateRun(ctx, runToUpdateParams(run))
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *SQLiteDatabase) GetRun(ctx context.Context, id string) (*model.Run, error) {
	row, err := db.q.GetRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) GetRunByExecutionID(ctx context.Context, executionID string) (*model.Run, error) {
	row, err := db.q.GetRunByExecutionID(ctx, &executionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) GetRunSummary(ctx context.Context) (*model.RunSummary, error) {
	row, err := db.q.GetRunSummary(ctx)
	if err != nil {
		return nil, err
	}
	return &model.RunSummary{
		Total:       row.Total,
		Success:     row.Success,
		Failed:      row.Failed,
		Missed:      row.Missed,
		LastFailure: row.EndedAt,
	}, nil
}

func (db *SQLiteDatabase) CountRunsFiltered(ctx context.Context, filter model.RunFilter) (int64, error) {
	return db.q.CountRunsFiltered(ctx, buildRunFilterArgs(filter))
}

// QueryRuns dispatches to one of 12 sqlc-generated queries, picked by
// (q.SortField, q.SortDirection). Each underlying query is a constant SQL
// string emitted by sqlc, so the call sites are static — no hand-built
// SQL leaks into the daemon.
func (db *SQLiteDatabase) QueryRuns(ctx context.Context, q RunQuery) ([]model.Run, error) {
	filter := buildRunFilterArgs(q.Filter)
	params := sqlcdb.QueryRunsCreatedAtAscParams{
		StatusSet:         filter.StatusSet,
		TaskNameFilter:    filter.TaskNameFilter,
		SearchFilter:      filter.SearchFilter,
		SearchPattern:     filter.SearchPattern,
		CreatedAfter:      filter.CreatedAfter,
		CreatedBefore:     filter.CreatedBefore,
		TriggeredByFilter: filter.TriggeredByFilter,
		ExitCodeMin:       filter.ExitCodeMin,
		ExitCodeMax:       filter.ExitCodeMax,
		RetriesOnly:       filter.RetriesOnly,
		MatchFailure:      filter.MatchFailure,
		RowsLimit:         int64(q.Limit),
		RowsOffset:        int64(q.Offset),
	}
	return dispatchQueryRuns(ctx, db.q, q.SortField, q.SortDirection, params)
}

// DeleteRun hard-deletes a single run by id, bypassing the soft-delete
// window. Used by retention sweeps; soft-delete-aware paths go through
// SoftDeleteRuns instead.
func (db *SQLiteDatabase) DeleteRun(ctx context.Context, id string) error {
	return db.q.DeleteRun(ctx, id)
}

// DeleteRunsByIDs hard-deletes the given rows in a single batch. Used by
// retention/purge callers as the second half of a select-then-delete
// sequence: they must remove the on-disk log files for these rows first, so
// a crash between the two steps leaves an orphan row (harmless) rather than
// an orphan log file (a permanent disk leak). A no-op for an empty slice.
func (db *SQLiteDatabase) DeleteRunsByIDs(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return db.q.DeleteRunsByIDs(ctx, ids)
}

// RunRef is the minimum identifying tuple needed to resolve a run's log path
// on disk. Returned by bulk-selector storage methods so callers can publish
// events or clean up logs without re-querying the run table.
type RunRef struct {
	ID        string
	TaskName  string
	CreatedAt time.Time
}

// runRefsFrom projects the sqlc rows of the bulk-selector queries, which all
// SELECT exactly id, task_name, created_at, into RunRefs.
func runRefsFrom[T ~struct {
	ID        string    `json:"id"`
	TaskName  string    `json:"task_name"`
	CreatedAt time.Time `json:"created_at"`
}](rows []T) []RunRef {
	out := make([]RunRef, len(rows))
	for i, r := range rows {
		out[i] = RunRef(r)
	}
	return out
}

// SoftDeleteRuns marks every run matched by sel (and currently terminal +
// not already soft-deleted) with the supplied deletion timestamp. Returns
// the affected rows as lightweight refs so callers can publish run.deleted
// events without a follow-up query.
func (db *SQLiteDatabase) SoftDeleteRuns(ctx context.Context, sel model.RunSelector, deletedAt time.Time) ([]RunRef, error) {
	deletedAt = deletedAt.UTC()
	if sel.MatchAll {
		args := buildRunFilterArgs(sel.Filter)
		rows, err := db.q.SoftDeleteRunsByFilter(ctx, sqlcdb.SoftDeleteRunsByFilterParams{
			DeletedAt:         &deletedAt,
			StatusPhase:       model.PhaseEnded,
			StatusSet:         args.StatusSet,
			TaskNameFilter:    args.TaskNameFilter,
			SearchFilter:      args.SearchFilter,
			SearchPattern:     args.SearchPattern,
			CreatedAfter:      args.CreatedAfter,
			CreatedBefore:     args.CreatedBefore,
			TriggeredByFilter: args.TriggeredByFilter,
			ExitCodeMin:       args.ExitCodeMin,
			ExitCodeMax:       args.ExitCodeMax,
			RetriesOnly:       args.RetriesOnly,
			MatchFailure:      args.MatchFailure,
			ExceptIds:         exceptIDsForSlice(sel.ExceptIDs),
		})
		if err != nil {
			return nil, err
		}
		return runRefsFrom(rows), nil
	}
	rows, err := db.q.SoftDeleteRunsByIDs(ctx, sqlcdb.SoftDeleteRunsByIDsParams{
		DeletedAt:   &deletedAt,
		StatusPhase: model.PhaseEnded,
		Ids:         sel.IDs,
	})
	if err != nil {
		return nil, err
	}
	return runRefsFrom(rows), nil
}

// RestoreRuns clears deleted_at for every soft-deleted run matched by sel
// and returns the full restored rows so the caller can re-emit run.updated
// events that bring the rows back in connected UIs.
func (db *SQLiteDatabase) RestoreRuns(ctx context.Context, sel model.RunSelector) ([]model.Run, error) {
	if sel.MatchAll {
		args := buildRunFilterArgs(sel.Filter)
		rows, err := db.q.RestoreRunsByFilter(ctx, sqlcdb.RestoreRunsByFilterParams{
			StatusSet:         args.StatusSet,
			TaskNameFilter:    args.TaskNameFilter,
			SearchFilter:      args.SearchFilter,
			SearchPattern:     args.SearchPattern,
			CreatedAfter:      args.CreatedAfter,
			CreatedBefore:     args.CreatedBefore,
			TriggeredByFilter: args.TriggeredByFilter,
			ExitCodeMin:       args.ExitCodeMin,
			ExitCodeMax:       args.ExitCodeMax,
			RetriesOnly:       args.RetriesOnly,
			MatchFailure:      args.MatchFailure,
			ExceptIds:         exceptIDsForSlice(sel.ExceptIDs),
		})
		if err != nil {
			return nil, err
		}
		return runsFromRows(rows), nil
	}
	rows, err := db.q.RestoreRunsByIDs(ctx, sel.IDs)
	if err != nil {
		return nil, err
	}
	return runsFromRows(rows), nil
}

// ResolveSelectorIDs returns the IDs of non-deleted runs matched by sel,
// optionally constrained to a status (use "" for any). Used by bulk
// cancel/rerun which need IDs to drive per-run actions.
func (db *SQLiteDatabase) ResolveSelectorIDs(ctx context.Context, sel model.RunSelector, statusFilter string) ([]RunRef, error) {
	if sel.MatchAll {
		args := buildRunFilterArgs(sel.Filter)
		rows, err := db.q.ResolveSelectorIDsByFilter(ctx, sqlcdb.ResolveSelectorIDsByFilterParams{
			StatusSet:         args.StatusSet,
			TaskNameFilter:    args.TaskNameFilter,
			SearchFilter:      args.SearchFilter,
			SearchPattern:     args.SearchPattern,
			CreatedAfter:      args.CreatedAfter,
			CreatedBefore:     args.CreatedBefore,
			TriggeredByFilter: args.TriggeredByFilter,
			ExitCodeMin:       args.ExitCodeMin,
			ExitCodeMax:       args.ExitCodeMax,
			RetriesOnly:       args.RetriesOnly,
			MatchFailure:      args.MatchFailure,
			BulkStatusFilter:  nullable(statusFilter),
			ExceptIds:         exceptIDsForSlice(sel.ExceptIDs),
		})
		if err != nil {
			return nil, err
		}
		return runRefsFrom(rows), nil
	}
	rows, err := db.q.ResolveSelectorIDsByIDs(ctx, sqlcdb.ResolveSelectorIDsByIDsParams{
		Ids:              sel.IDs,
		BulkStatusFilter: nullable(statusFilter),
	})
	if err != nil {
		return nil, err
	}
	return runRefsFrom(rows), nil
}

// SelectExpiredSoftDeletes returns refs for every soft-deleted row whose
// deleted_at is older than ttl ago (use ttl=0 to select everything currently
// soft-deleted, for the boot-time drain) without deleting anything. Callers
// must remove the referenced log files before hard-deleting the rows via
// DeleteRunsByIDs — see the SQL query's comment for why the order matters.
func (db *SQLiteDatabase) SelectExpiredSoftDeletes(ctx context.Context, ttl time.Duration) ([]RunRef, error) {
	cutoff := time.Now().UTC().Add(-ttl)
	rows, err := db.q.SelectExpiredSoftDeletes(ctx, &cutoff)
	if err != nil {
		return nil, err
	}
	return runRefsFrom(rows), nil
}

// SelectOldRuns returns the runs that KeepFor/KeepRuns retention would evict
// for task, without deleting anything. Callers must remove the runs' log
// files before hard-deleting the rows via DeleteRunsByIDs — see
// SelectExpiredSoftDeletes for why the order matters.
func (db *SQLiteDatabase) SelectOldRuns(ctx context.Context, task *model.Task) ([]model.Run, error) {
	uniqueRuns := make(map[string]model.Run)

	if task.KeepFor > 0 {
		cutoff := time.Now().UTC().Add(-task.KeepFor)
		rows, err := db.q.SelectOldRunsByAge(ctx, sqlcdb.SelectOldRunsByAgeParams{
			TaskName:  task.Name,
			CreatedAt: cutoff,
			Limit:     int64(retentionBatchSize),
		})
		if err != nil {
			return nil, fmt.Errorf("query retention days for %s: %w", task.Name, err)
		}
		collectRunsByID(uniqueRuns, rows)
	}

	if len(uniqueRuns) < retentionBatchSize && task.KeepRuns != nil {
		remaining := retentionBatchSize - len(uniqueRuns)
		rows, err := db.q.SelectOldRunsByCount(ctx, sqlcdb.SelectOldRunsByCountParams{
			TaskName: task.Name,
			Limit:    int64(remaining),
			Offset:   int64(*task.KeepRuns),
		})
		if err != nil {
			return nil, fmt.Errorf("query retention runs for %s: %w", task.Name, err)
		}
		collectRunsByID(uniqueRuns, rows)
	}

	return slices.AppendSeq(make([]model.Run, 0, len(uniqueRuns)), maps.Values(uniqueRuns)), nil
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	return db.q.MarkCrashedRuns(ctx, &now)
}

func (db *SQLiteDatabase) GetPendingRuns(ctx context.Context) ([]model.Run, error) {
	rows, err := db.q.GetPendingRuns(ctx)
	if err != nil {
		return nil, err
	}
	return runsFromRows(rows), nil
}

func (db *SQLiteDatabase) GetLastRunByTask(ctx context.Context, taskName string) (*model.Run, error) {
	row, err := db.q.GetLastRunByTask(ctx, taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error {
	return db.q.EnsureTaskRegistered(ctx, sqlcdb.EnsureTaskRegisteredParams{
		TaskName:    taskName,
		FirstSeenAt: firstSeen,
	})
}

func (db *SQLiteDatabase) GetTaskRegistration(ctx context.Context, taskName string) (*model.TaskRegistration, error) {
	r, err := db.q.GetTaskRegistration(ctx, taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model.TaskRegistration{
		TaskName:    r.TaskName,
		FirstSeenAt: r.FirstSeenAt,
		PausedAt:    r.PausedAt,
		ResumedAt:   r.ResumedAt,
	}, nil
}

func (db *SQLiteDatabase) PauseTaskSchedule(ctx context.Context, taskName string, at time.Time) error {
	return db.q.PauseTaskSchedule(ctx, sqlcdb.PauseTaskScheduleParams{TaskName: taskName, PausedAt: at})
}

func (db *SQLiteDatabase) ResumeTaskSchedule(ctx context.Context, taskName string, at time.Time) error {
	return db.q.ResumeTaskSchedule(ctx, sqlcdb.ResumeTaskScheduleParams{TaskName: taskName, ResumedAt: &at})
}

func (db *SQLiteDatabase) ListPausedTaskSchedules(ctx context.Context) (map[string]time.Time, error) {
	rows, err := db.q.ListPausedTaskSchedules(ctx)
	if err != nil {
		return nil, err
	}
	paused := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		if r.PausedAt != nil {
			paused[r.TaskName] = *r.PausedAt
		}
	}
	return paused, nil
}

func (db *SQLiteDatabase) GetTaskBootID(ctx context.Context, taskName string) (string, error) {
	id, err := db.q.GetTaskBootID(ctx, taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (db *SQLiteDatabase) SetTaskBootID(ctx context.Context, taskName, bootID string) error {
	return db.q.SetTaskBootID(ctx, sqlcdb.SetTaskBootIDParams{TaskName: taskName, BootID: bootID})
}

func (db *SQLiteDatabase) GetConfigValue(ctx context.Context, key string) (string, bool, error) {
	val, err := db.q.GetConfigValue(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (db *SQLiteDatabase) SetConfigValue(ctx context.Context, key, value string) error {
	return db.q.SetConfigValue(ctx, sqlcdb.SetConfigValueParams{Key: key, Value: value})
}

func (db *SQLiteDatabase) Close() error {
	return db.db.Close()
}

// ShrinkMemory releases heap held by SQLite's page cache back to the allocator
// (PRAGMA shrink_memory). The runtime MemoryReclaimer calls this periodically
// so idle RSS tracks the working set instead of the high-water mark. Cheap and
// safe: it only drops clean cached pages, which are re-read on demand.
func (db *SQLiteDatabase) ShrinkMemory(ctx context.Context) error {
	_, err := db.db.ExecContext(ctx, "PRAGMA shrink_memory;")
	return err
}

func (db *SQLiteDatabase) UpsertPendingLogUpload(ctx context.Context, rec model.PendingLogUpload) error {
	return db.q.UpsertPendingLogUpload(ctx, sqlcdb.UpsertPendingLogUploadParams{
		ExecutionID:    rec.ExecutionID,
		UploadUrl:      rec.UploadURL,
		LogPath:        rec.LogPath,
		InsertedAtUnix: rec.InsertedAt,
	})
}

func (db *SQLiteDatabase) DeletePendingLogUpload(ctx context.Context, executionID string) error {
	return db.q.DeletePendingLogUpload(ctx, executionID)
}

func (db *SQLiteDatabase) ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error) {
	rows, err := db.q.ListPendingLogUploads(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.PendingLogUpload, 0, len(rows))
	for _, r := range rows {
		out = append(out, pendingLogUploadFromRow(r))
	}
	return out, nil
}
