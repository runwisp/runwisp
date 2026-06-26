// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	_ "embed" // embeds schema.sql into schemaSQL via the //go:embed directive below
	"errors"
	"fmt"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // registers the SQLite driver for database/sql

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("record not found")

const (
	ConfigKeyFingerprint = "fingerprint"

	MaxSearchQueryLength = 100
	RetentionBatchSize   = 1000
	SQLiteBusyTimeout    = 5000
	SQLiteMaxOpenConns   = 1
	// SQLiteCacheSizeKiB pins the page cache. A negative cache_size is read by
	// SQLite as KiB rather than pages, so this caps the cache at ~2 MiB instead
	// of letting modernc's allocator drift up to (and hold) its high-water mark.
	// Plenty for a metadata-only store; log bodies live on disk, not in SQLite.
	SQLiteCacheSizeKiB = -2000
	// SQLiteSoftHeapLimitBytes caps the SQLite allocator's heap (16 MiB), forcing
	// it to spill caches rather than grow unbounded during big list/retention
	// scans. Bounds worst-case SQLite RSS at steady state.
	SQLiteSoftHeapLimitBytes = 16 << 20
)

// RunRepository defines the interface for run persistence.
type RunRepository interface {
	CreateRun(ctx context.Context, run *model.Run) error
	UpdateRun(ctx context.Context, run *model.Run) error
	GetRun(ctx context.Context, id string) (*model.Run, error)
	GetRunByExternalExecutionID(ctx context.Context, externalExecutionID string) (*model.Run, error)
	CountRuns(ctx context.Context, taskName string) (int64, error)
	CountRunsFiltered(ctx context.Context, filter model.RunFilter) (int64, error)
	QueryRuns(ctx context.Context, q RunQuery) ([]model.Run, error)
	DeleteRun(ctx context.Context, id string) error
	DeleteOldRuns(ctx context.Context, task *model.Task) ([]model.Run, error)
	MarkCrashedRuns(ctx context.Context) (int64, error)
	GetPendingRuns(ctx context.Context) ([]model.Run, error)
	GetLastRunByTask(ctx context.Context, taskName string) (*model.Run, error)
	GetRunSummary(ctx context.Context) (*model.RunSummary, error)
	EnsureTaskRegistered(ctx context.Context, taskName string, firstSeen time.Time) error
	GetTaskRegistration(ctx context.Context, taskName string) (*model.TaskRegistration, error)
	SoftDeleteRuns(ctx context.Context, sel model.RunSelector, deletedAt time.Time) ([]RunRef, error)
	RestoreRuns(ctx context.Context, sel model.RunSelector) ([]model.Run, error)
	ResolveSelectorIDs(ctx context.Context, sel model.RunSelector, statusFilter string) ([]RunRef, error)
	PurgeExpiredSoftDeletes(ctx context.Context, ttl time.Duration) ([]RunRef, error)
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
	DeletePendingLogUpload(ctx context.Context, externalExecutionID string) error
	ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error)
}

// Database is the full persistent store for the daemon: runs + configuration + notifications.
type Database interface {
	RunRepository
	ConfigRepository
	NotificationRepository
	PendingLogUploadRepository
}

// SQLiteDatabase wraps persistence concerns for runs and configuration. The
// raw *sql.DB is retained for the single hand-written read path (QueryRuns)
// whose ORDER BY tail is composed at runtime; every other query routes
// through the generated sqlcdb.Queries.
type SQLiteDatabase struct {
	db *sql.DB
	q  *sqlcdb.Queries
}

// New opens (and migrates) the SQLite database.
func New(dbPath string) (Database, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite uses single-writer serialized mode, limit to 1 connection.
	db.SetMaxOpenConns(SQLiteMaxOpenConns)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=" + strconv.Itoa(SQLiteBusyTimeout) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}
	// Bound SQLite's memory: pin the page cache, cap the allocator's heap, and
	// keep that bounded cache the only buffer (no growing mmap region). Lowers
	// idle RSS — see SQLiteCacheSizeKiB / SQLiteSoftHeapLimitBytes.
	if _, err := db.Exec("PRAGMA cache_size=" + strconv.Itoa(SQLiteCacheSizeKiB) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set cache_size: %w", err)
	}
	if _, err := db.Exec("PRAGMA soft_heap_limit=" + strconv.Itoa(SQLiteSoftHeapLimitBytes) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set soft_heap_limit: %w", err)
	}
	if _, err := db.Exec("PRAGMA mmap_size=0;"); err != nil {
		return nil, fmt.Errorf("failed to disable mmap: %w", err)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &SQLiteDatabase{db: db, q: sqlcdb.New(db)}, nil
}

func (db *SQLiteDatabase) CreateRun(ctx context.Context, run *model.Run) error {
	return db.q.CreateRun(ctx, runToCreateParams(run))
}

func (db *SQLiteDatabase) UpdateRun(ctx context.Context, run *model.Run) error {
	return db.q.UpdateRun(ctx, runToUpdateParams(run))
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

func (db *SQLiteDatabase) GetRunByExternalExecutionID(ctx context.Context, externalExecutionID string) (*model.Run, error) {
	row, err := db.q.GetRunByExternalExecutionID(ctx, &externalExecutionID)
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
		LastFailure: row.EndAt,
	}, nil
}

func (db *SQLiteDatabase) CountRuns(ctx context.Context, taskName string) (int64, error) {
	return db.q.CountRuns(ctx, taskName)
}

func (db *SQLiteDatabase) CountRunsFiltered(ctx context.Context, filter model.RunFilter) (int64, error) {
	args := buildRunFilterArgs(filter)
	return db.q.CountRunsFiltered(ctx, sqlcdb.CountRunsFilteredParams{
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
	})
}

// QueryRuns dispatches to one of 12 sqlc-generated queries, picked by
// (q.SortField, q.SortDirection). Each underlying query is a constant SQL
// string emitted by sqlc, so the call sites are static — no hand-built
// SQL leaks into the daemon. All 12 row types are structurally identical
// (the SELECT list is shared); they're collapsed onto QueryRunsCreatedAtAscRow
// via Go's struct conversion for a single conversion path to model.Run.
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

// RunRef is the minimum identifying tuple needed to resolve a run's log path
// on disk. Returned by bulk-selector storage methods so callers can publish
// events or clean up logs without re-querying the run table.
type RunRef struct {
	ID        string
	TaskName  string
	CreatedAt time.Time
}

// SoftDeleteRuns marks every run matched by sel (and currently terminal +
// not already soft-deleted) with the supplied deletion timestamp. Returns
// the affected rows as lightweight refs so callers can publish run.deleted
// events without a follow-up query.
func (db *SQLiteDatabase) SoftDeleteRuns(ctx context.Context, sel model.RunSelector, deletedAt time.Time) ([]RunRef, error) {
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
			ExceptIds:         exceptIDsForSlice(sel.ExceptIDs),
		})
		if err != nil {
			return nil, err
		}
		out := make([]RunRef, 0, len(rows))
		for _, r := range rows {
			out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
		}
		return out, nil
	}
	rows, err := db.q.SoftDeleteRunsByIDs(ctx, sqlcdb.SoftDeleteRunsByIDsParams{
		DeletedAt:   &deletedAt,
		StatusPhase: model.PhaseEnded,
		Ids:         sel.IDs,
	})
	if err != nil {
		return nil, err
	}
	out := make([]RunRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// RestoreRuns clears deleted_at for every soft-deleted run matched by sel
// and returns the full restored rows so the caller can re-emit run.updated
// events that bring the rows back in connected UIs.
func (db *SQLiteDatabase) RestoreRuns(ctx context.Context, sel model.RunSelector) ([]model.Run, error) {
	if sel.MatchAll {
		args := buildRunFilterArgs(sel.Filter)
		exceptIDs := exceptIDsForSlice(sel.ExceptIDs)
		if err := db.q.RestoreRunsByFilter(ctx, sqlcdb.RestoreRunsByFilterParams{
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
			ExceptIds:         exceptIDs,
		}); err != nil {
			return nil, err
		}
		rows, err := db.q.SelectRestoredRunsByFilter(ctx, sqlcdb.SelectRestoredRunsByFilterParams{
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
			ExceptIds:         exceptIDs,
		})
		if err != nil {
			return nil, err
		}
		return runsFromRows(rows), nil
	}
	if err := db.q.RestoreRunsByIDs(ctx, sel.IDs); err != nil {
		return nil, err
	}
	rows, err := db.q.SelectRestoredRunsByIDs(ctx, sel.IDs)
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
			BulkStatusFilter:  nullable(statusFilter),
			ExceptIds:         exceptIDsForSlice(sel.ExceptIDs),
		})
		if err != nil {
			return nil, err
		}
		out := make([]RunRef, 0, len(rows))
		for _, r := range rows {
			out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
		}
		return out, nil
	}
	rows, err := db.q.ResolveSelectorIDsByIDs(ctx, sqlcdb.ResolveSelectorIDsByIDsParams{
		Ids:              sel.IDs,
		BulkStatusFilter: nullable(statusFilter),
	})
	if err != nil {
		return nil, err
	}
	out := make([]RunRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// PurgeExpiredSoftDeletes hard-deletes every soft-deleted row whose
// deleted_at is older than ttl ago (use ttl=0 to drain all on boot).
// Returns refs so the caller can wipe the matching log files.
func (db *SQLiteDatabase) PurgeExpiredSoftDeletes(ctx context.Context, ttl time.Duration) ([]RunRef, error) {
	cutoff := time.Now().Add(-ttl)
	rows, err := db.q.PurgeExpiredSoftDeletes(ctx, &cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]RunRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

func (db *SQLiteDatabase) DeleteOldRuns(ctx context.Context, task *model.Task) ([]model.Run, error) {
	uniqueRuns := make(map[string]model.Run)

	if task.KeepFor > 0 {
		cutoff := time.Now().Add(-task.KeepFor)
		rows, err := db.q.SelectOldRunsByAge(ctx, sqlcdb.SelectOldRunsByAgeParams{
			TaskName:  task.Name,
			CreatedAt: cutoff,
			Limit:     int64(RetentionBatchSize),
		})
		if err != nil {
			return nil, fmt.Errorf("query retention days for %s: %w", task.Name, err)
		}
		collectRunsByID(uniqueRuns, rows)
	}

	if len(uniqueRuns) < RetentionBatchSize && task.KeepRuns > 0 {
		remaining := RetentionBatchSize - len(uniqueRuns)
		rows, err := db.q.SelectOldRunsByCount(ctx, sqlcdb.SelectOldRunsByCountParams{
			TaskName: task.Name,
			Limit:    int64(remaining),
			Offset:   int64(task.KeepRuns),
		})
		if err != nil {
			return nil, fmt.Errorf("query retention runs for %s: %w", task.Name, err)
		}
		collectRunsByID(uniqueRuns, rows)
	}

	if len(uniqueRuns) == 0 {
		return []model.Run{}, nil
	}

	ids := make([]string, 0, len(uniqueRuns))
	finalRuns := make([]model.Run, 0, len(uniqueRuns))
	for id, run := range uniqueRuns {
		ids = append(ids, id)
		finalRuns = append(finalRuns, run)
	}

	if err := db.q.DeleteRunsByIDs(ctx, ids); err != nil {
		return nil, fmt.Errorf("delete old runs for %s: %w", task.Name, err)
	}

	return finalRuns, nil
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns(ctx context.Context) (int64, error) {
	now := time.Now()
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
	return &model.TaskRegistration{TaskName: r.TaskName, FirstSeenAt: r.FirstSeenAt}, nil
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
		ExternalExecutionID: rec.ExternalExecutionID,
		UploadUrl:           rec.UploadURL,
		LogPath:             rec.LogPath,
		InsertedAt:          rec.InsertedAt,
	})
}

func (db *SQLiteDatabase) DeletePendingLogUpload(ctx context.Context, externalExecutionID string) error {
	return db.q.DeletePendingLogUpload(ctx, externalExecutionID)
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
