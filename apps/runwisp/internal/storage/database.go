// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the SQLite driver for database/sql

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

//go:embed schema.sql
var schemaSQL string

const (
	MaxSearchQueryLength = 100
	RetentionBatchSize   = 1000
	SQLiteBusyTimeout    = 5000
	SQLiteMaxOpenConns   = 1
)

// RunRepository defines the interface for run persistence.
type RunRepository interface {
	CreateRun(run *sqlcdb.Run) error
	UpdateRun(run *sqlcdb.Run) error
	GetRun(id string) (*sqlcdb.Run, error)
	GetRunByExternalExecutionID(externalExecutionID string) (*sqlcdb.Run, error)
	CountRuns(taskName string) (int64, error)
	CountRunsFiltered(status, taskName, searchQuery string) (int64, error)
	QueryRuns(taskName string, limit, offset int, status string, sortField SortColumn, sortDirection SortDirection, searchQuery string) ([]sqlcdb.Run, error)
	DeleteRun(id string) error
	DeleteOldRuns(task *model.Task) ([]sqlcdb.Run, error)
	MarkCrashedRuns() (int64, error)
	GetPendingRuns() ([]sqlcdb.Run, error)
	GetLastRunByTask(taskName string) (*sqlcdb.Run, error)
	GetRunSummary() (*sqlcdb.RunSummary, error)
	EnsureTaskRegistered(taskName string, firstSeen time.Time) error
	GetTaskRegistration(taskName string) (*model.TaskRegistration, error)
	SoftDeleteRuns(sel model.RunSelector, deletedAt time.Time) ([]RunRef, error)
	RestoreRuns(sel model.RunSelector) ([]sqlcdb.Run, error)
	ResolveSelectorIDs(sel model.RunSelector, statusFilter string) ([]RunRef, error)
	PurgeExpiredSoftDeletes(ttl time.Duration) ([]RunRef, error)
	Close() error
}

// SQLiteDatabase wraps persistence concerns for runs and configuration. The
// raw *sql.DB is retained for hand-written queries (soft-delete, QueryRuns)
// that use json_each() or dynamic ORDER BY tails sqlc cannot model; everything
// else routes through the generated sqlcdb.Queries.
type SQLiteDatabase struct {
	db *sql.DB
	q  *sqlcdb.Queries
}

// New opens (and migrates) the SQLite database.
// logOutput is accepted for API compatibility but no longer used (we rely on slog).
func New(dbPath string, logOutput io.Writer) (Database, error) {
	_ = logOutput
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

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &SQLiteDatabase{db: db, q: sqlcdb.New(db)}, nil
}

// bgCtx returns a background context for sqlc calls. The Database interface
// is intentionally context-less today; if a long-running caller needs to
// cancel a query they should kill the daemon.
func bgCtx() context.Context { return context.Background() }

const runColumns = `id, external_execution_id, task_name, status, end_reason, exit_code,
start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index`

func (db *SQLiteDatabase) CreateRun(run *sqlcdb.Run) error {
	return db.q.CreateRun(bgCtx(), runToCreateParams(run))
}

func (db *SQLiteDatabase) UpdateRun(run *sqlcdb.Run) error {
	return db.q.UpdateRun(bgCtx(), runToUpdateParams(run))
}

func scanRun(scanner interface {
	Scan(dest ...any) error
}) (*sqlcdb.Run, error) {
	var run sqlcdb.Run
	err := scanner.Scan(
		&run.ID, &run.ExternalExecutionID, &run.TaskName, &run.Status, &run.EndReason, &run.ExitCode,
		&run.StartAt, &run.EndAt, &run.TriggeredBy, &run.CreatedAt, &run.RetryAttempt, &run.RetryOfRunID, &run.InstanceIndex,
	)
	if err != nil {
		return nil, err
	}
	return &run, nil
}

func collectRows[T any](rows *sql.Rows, scan func(interface{ Scan(...any) error }) (*T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *item)
	}
	return out, rows.Err()
}

func (db *SQLiteDatabase) GetRun(id string) (*sqlcdb.Run, error) {
	row, err := db.q.GetRun(bgCtx(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromSqlcdb(row), nil
}

func (db *SQLiteDatabase) GetRunByExternalExecutionID(externalExecutionID string) (*sqlcdb.Run, error) {
	row, err := db.q.GetRunByExternalExecutionID(bgCtx(), &externalExecutionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromSqlcdb(row), nil
}

func (db *SQLiteDatabase) GetRunSummary() (*sqlcdb.RunSummary, error) {
	row, err := db.q.GetRunSummary(bgCtx())
	if err != nil {
		return nil, err
	}
	summary := &sqlcdb.RunSummary{
		Total:   row.Total,
		Success: row.Success,
		Failed:  row.Failed,
	}
	// last_failure is emitted as interface{} because MAX(CASE WHEN ...) is
	// nullable from SQLite's perspective. modernc/sqlite hands back time.Time
	// for DATETIME columns; preserve that when present.
	if t, ok := row.LastFailure.(time.Time); ok {
		summary.LastFailure = &t
	}
	return summary, nil
}

func (db *SQLiteDatabase) CountRuns(taskName string) (int64, error) {
	return db.q.CountRuns(bgCtx(), taskName)
}

type queryBuilder struct {
	where []string
	args  []any
}

func (q *queryBuilder) add(clause string, args ...any) {
	q.where = append(q.where, clause)
	q.args = append(q.args, args...)
}

func (q *queryBuilder) whereSQL() string {
	if len(q.where) == 0 {
		return ""
	}
	return wherePrefix + strings.Join(q.where, " AND ")
}

func applyStatusFilter(q *queryBuilder, status string) {
	if status == "" {
		return
	}
	switch sqlcdb.EndReason(status) {
	case sqlcdb.ReasonSuccess, sqlcdb.ReasonFailed, sqlcdb.ReasonStopped, sqlcdb.ReasonTimeout, sqlcdb.ReasonCrashed, sqlcdb.ReasonSkipped, sqlcdb.ReasonLogOverflow:
		q.add("end_reason = ?", status)
	default:
		q.add("status = ?", status)
	}
}

func applySearchFilter(q *queryBuilder, searchQuery string) {
	if searchQuery == "" {
		return
	}
	if len(searchQuery) > MaxSearchQueryLength {
		searchQuery = searchQuery[:MaxSearchQueryLength]
	}
	searchQuery = strings.ReplaceAll(searchQuery, "%", "")
	searchQuery = strings.ReplaceAll(searchQuery, "_", "")
	pattern := "%" + searchQuery + "%"
	q.add("(task_name LIKE ? OR id LIKE ?)", pattern, pattern)
}

func (db *SQLiteDatabase) CountRunsFiltered(status, taskName, searchQuery string) (int64, error) {
	args := runFilterGateArgs(model.RunFilter{
		Status:   status,
		TaskName: taskName,
		Search:   searchQuery,
	})
	var count int64
	err := db.db.QueryRow(countRunsFilteredSQL, args...).Scan(&count)
	return count, err
}

func (db *SQLiteDatabase) QueryRuns(taskName string, limit, offset int, status string, sortField SortColumn, sortDirection SortDirection, searchQuery string) ([]sqlcdb.Run, error) {
	var q queryBuilder
	if taskName != "" {
		q.add("task_name = ?", taskName)
	}
	applyStatusFilter(&q, status)
	applySearchFilter(&q, searchQuery)

	order, err := buildOrderClause(sortField, sortDirection)
	if err != nil {
		return nil, err
	}
	args := append(q.args, limit, offset)

	query := selectRunsSQL(q.whereSQL(), "ORDER BY "+order+" LIMIT ? OFFSET ?")
	rows, err := db.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanRun)
}

// DeleteRun hard-deletes a single run by id, bypassing the soft-delete
// window. Used by retention sweeps; soft-delete-aware paths go through
// SoftDeleteRuns instead.
func (db *SQLiteDatabase) DeleteRun(id string) error {
	return db.q.DeleteRun(bgCtx(), id)
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
func (db *SQLiteDatabase) SoftDeleteRuns(sel model.RunSelector, deletedAt time.Time) ([]RunRef, error) {
	if sel.MatchAll {
		args := []any{deletedAt, sqlcdb.PhaseEnded}
		args = append(args, runFilterGateArgs(sel.Filter)...)
		args = append(args, idsJSON(sel.ExceptIDs))
		rows, err := db.db.Query(softDeleteByFilterSQL, args...)
		if err != nil {
			return nil, err
		}
		return scanRunRefs(rows)
	}
	rows, err := db.db.Query(softDeleteByIDsSQL, deletedAt, sqlcdb.PhaseEnded, idsJSON(sel.IDs))
	if err != nil {
		return nil, err
	}
	return scanRunRefs(rows)
}

// RestoreRuns clears deleted_at for every soft-deleted run matched by sel
// and returns the full restored rows so the caller can re-emit run.updated
// events that bring the rows back in connected UIs.
func (db *SQLiteDatabase) RestoreRuns(sel model.RunSelector) ([]sqlcdb.Run, error) {
	if sel.MatchAll {
		args := runFilterGateArgs(sel.Filter)
		args = append(args, idsJSON(sel.ExceptIDs))
		if _, err := db.db.Exec(restoreByFilterSQL, args...); err != nil {
			return nil, err
		}
		rows, err := db.db.Query(selectRestoredByFilterSQL, args...)
		if err != nil {
			return nil, err
		}
		return collectRows(rows, scanRun)
	}
	idsArg := idsJSON(sel.IDs)
	if _, err := db.db.Exec(restoreByIDsSQL, idsArg); err != nil {
		return nil, err
	}
	rows, err := db.db.Query(selectRestoredByIDsSQL, idsArg)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanRun)
}

// ResolveSelectorIDs returns the IDs of non-deleted runs matched by sel,
// optionally constrained to a status (use "" for any). Used by bulk
// cancel/rerun which need IDs to drive per-run actions.
func (db *SQLiteDatabase) ResolveSelectorIDs(sel model.RunSelector, statusFilter string) ([]RunRef, error) {
	if sel.MatchAll {
		args := runFilterGateArgs(sel.Filter)
		args = append(args, idsJSON(sel.ExceptIDs), statusFilter, statusFilter)
		rows, err := db.db.Query(resolveByFilterSQL, args...)
		if err != nil {
			return nil, err
		}
		return scanRunRefs(rows)
	}
	rows, err := db.db.Query(resolveByIDsSQL, idsJSON(sel.IDs), statusFilter, statusFilter)
	if err != nil {
		return nil, err
	}
	return scanRunRefs(rows)
}

// PurgeExpiredSoftDeletes hard-deletes every soft-deleted row whose
// deleted_at is older than ttl ago (use ttl=0 to drain all on boot).
// Returns refs so the caller can wipe the matching log files.
func (db *SQLiteDatabase) PurgeExpiredSoftDeletes(ttl time.Duration) ([]RunRef, error) {
	cutoff := time.Now().Add(-ttl)
	rows, err := db.q.PurgeExpiredSoftDeletes(bgCtx(), &cutoff)
	if err != nil {
		return nil, err
	}
	out := make([]RunRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunRef{ID: r.ID, TaskName: r.TaskName, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

func scanRunRefs(rows *sql.Rows) ([]RunRef, error) {
	defer rows.Close()
	var out []RunRef
	for rows.Next() {
		var ref RunRef
		if err := rows.Scan(&ref.ID, &ref.TaskName, &ref.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (db *SQLiteDatabase) DeleteOldRuns(task *model.Task) ([]sqlcdb.Run, error) {
	uniqueRuns := make(map[string]sqlcdb.Run)
	ctx := bgCtx()

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
		for _, r := range rows {
			uniqueRuns[r.ID] = runFromSqlcdb(r)
		}
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
		for _, r := range rows {
			uniqueRuns[r.ID] = runFromSqlcdb(r)
		}
	}

	if len(uniqueRuns) == 0 {
		return []sqlcdb.Run{}, nil
	}

	ids := make([]string, 0, len(uniqueRuns))
	finalRuns := make([]sqlcdb.Run, 0, len(uniqueRuns))
	for id, run := range uniqueRuns {
		ids = append(ids, id)
		finalRuns = append(finalRuns, run)
	}

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	if _, err := db.db.Exec(`DELETE FROM runs WHERE id IN (`+placeholders+`)`, args...); err != nil {
		return nil, fmt.Errorf("delete old runs for %s: %w", task.Name, err)
	}

	return finalRuns, nil
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns() (int64, error) {
	now := time.Now()
	return db.q.MarkCrashedRuns(bgCtx(), &now)
}

func (db *SQLiteDatabase) GetPendingRuns() ([]sqlcdb.Run, error) {
	rows, err := db.q.GetPendingRuns(bgCtx())
	if err != nil {
		return nil, err
	}
	out := make([]sqlcdb.Run, 0, len(rows))
	for _, r := range rows {
		out = append(out, runFromSqlcdb(r))
	}
	return out, nil
}

func (db *SQLiteDatabase) GetLastRunByTask(taskName string) (*sqlcdb.Run, error) {
	row, err := db.q.GetLastRunByTask(bgCtx(), taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromSqlcdb(row), nil
}

func (db *SQLiteDatabase) EnsureTaskRegistered(taskName string, firstSeen time.Time) error {
	return db.q.EnsureTaskRegistered(bgCtx(), sqlcdb.EnsureTaskRegisteredParams{
		TaskName:    taskName,
		FirstSeenAt: firstSeen,
	})
}

func (db *SQLiteDatabase) GetTaskRegistration(taskName string) (*model.TaskRegistration, error) {
	r, err := db.q.GetTaskRegistration(bgCtx(), taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model.TaskRegistration{TaskName: r.TaskName, FirstSeenAt: r.FirstSeenAt}, nil
}

func (db *SQLiteDatabase) GetConfigValue(key string) (string, bool, error) {
	val, err := db.q.GetConfigValue(bgCtx(), key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (db *SQLiteDatabase) SetConfigValue(key, value string) error {
	return db.q.SetConfigValue(bgCtx(), sqlcdb.SetConfigValueParams{Key: key, Value: value})
}

func (db *SQLiteDatabase) Close() error {
	return db.db.Close()
}

func (db *SQLiteDatabase) UpsertPendingLogUpload(rec sqlcdb.PendingLogUpload) error {
	return db.q.UpsertPendingLogUpload(bgCtx(), sqlcdb.UpsertPendingLogUploadParams{
		ExternalExecutionID: rec.ExternalExecutionID,
		UploadUrl:           rec.UploadUrl,
		LogPath:             rec.LogPath,
		InsertedAt:          rec.InsertedAt,
	})
}

func (db *SQLiteDatabase) DeletePendingLogUpload(externalExecutionID string) error {
	return db.q.DeletePendingLogUpload(bgCtx(), externalExecutionID)
}

func (db *SQLiteDatabase) ListPendingLogUploads() ([]sqlcdb.PendingLogUpload, error) {
	rows, err := db.q.ListPendingLogUploads(bgCtx())
	if err != nil {
		return nil, err
	}
	out := make([]sqlcdb.PendingLogUpload, 0, len(rows))
	for _, r := range rows {
		out = append(out, pendingLogUploadFromSqlcdb(r))
	}
	return out, nil
}
