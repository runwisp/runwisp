// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
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
	CreateRun(run *model.Run) error
	UpdateRun(run *model.Run) error
	GetRun(id string) (*model.Run, error)
	GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error)
	CountRuns(taskName string) (int64, error)
	CountRunsFiltered(status, taskName, searchQuery string) (int64, error)
	QueryRuns(taskName string, limit, offset int, status string, sortField SortColumn, sortDirection SortDirection, searchQuery string) ([]model.Run, error)
	DeleteRun(id string) error
	DeleteOldRuns(task *model.Task) ([]model.Run, error)
	MarkCrashedRuns() (int64, error)
	GetPendingRuns() ([]model.Run, error)
	GetLastRunByTask(taskName string) (*model.Run, error)
	GetRunSummary() (*model.RunSummary, error)
	EnsureTaskRegistered(taskName string, firstSeen time.Time) error
	GetTaskRegistration(taskName string) (*model.TaskRegistration, error)
	SoftDeleteRuns(sel model.RunSelector, deletedAt time.Time) ([]RunRef, error)
	RestoreRuns(sel model.RunSelector) ([]model.Run, error)
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

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return &SQLiteDatabase{db: db, q: sqlcdb.New(db)}, nil
}

const runColumns = `id, external_execution_id, task_name, status, end_reason, exit_code,
start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index`

func (db *SQLiteDatabase) CreateRun(run *model.Run) error {
	return db.q.CreateRun(context.Background(), runToCreateParams(run))
}

func (db *SQLiteDatabase) UpdateRun(run *model.Run) error {
	return db.q.UpdateRun(context.Background(), runToUpdateParams(run))
}

func scanRun(scanner interface {
	Scan(dest ...any) error
}) (*model.Run, error) {
	var run model.Run
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

func (db *SQLiteDatabase) GetRun(id string) (*model.Run, error) {
	row, err := db.q.GetRun(context.Background(), id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error) {
	row, err := db.q.GetRunByExternalExecutionID(context.Background(), &externalExecutionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) GetRunSummary() (*model.RunSummary, error) {
	row, err := db.q.GetRunSummary(context.Background())
	if err != nil {
		return nil, err
	}
	summary := &model.RunSummary{
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
	return db.q.CountRuns(context.Background(), taskName)
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
	return "WHERE " + strings.Join(q.where, " AND ")
}

func applyStatusFilter(q *queryBuilder, status string) {
	if status == "" {
		return
	}
	switch model.EndReason(status) {
	case model.ReasonSuccess, model.ReasonFailed, model.ReasonStopped, model.ReasonTimeout, model.ReasonCrashed, model.ReasonSkipped, model.ReasonLogOverflow:
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

func (db *SQLiteDatabase) QueryRuns(taskName string, limit, offset int, status string, sortField SortColumn, sortDirection SortDirection, searchQuery string) ([]model.Run, error) {
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
	return db.q.DeleteRun(context.Background(), id)
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
		args := []any{deletedAt, model.PhaseEnded}
		args = append(args, runFilterGateArgs(sel.Filter)...)
		args = append(args, idsJSON(sel.ExceptIDs))
		rows, err := db.db.Query(softDeleteByFilterSQL, args...)
		if err != nil {
			return nil, err
		}
		return scanRunRefs(rows)
	}
	rows, err := db.db.Query(softDeleteByIDsSQL, deletedAt, model.PhaseEnded, idsJSON(sel.IDs))
	if err != nil {
		return nil, err
	}
	return scanRunRefs(rows)
}

// RestoreRuns clears deleted_at for every soft-deleted run matched by sel
// and returns the full restored rows so the caller can re-emit run.updated
// events that bring the rows back in connected UIs.
func (db *SQLiteDatabase) RestoreRuns(sel model.RunSelector) ([]model.Run, error) {
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
	rows, err := db.q.PurgeExpiredSoftDeletes(context.Background(), &cutoff)
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

func (db *SQLiteDatabase) DeleteOldRuns(task *model.Task) ([]model.Run, error) {
	uniqueRuns := make(map[string]model.Run)
	ctx := context.Background()

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
			uniqueRuns[r.ID] = runFromRow(r)
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
			uniqueRuns[r.ID] = runFromRow(r)
		}
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

	if _, err := db.db.Exec(`DELETE FROM runs WHERE id IN (SELECT value FROM json_each(?))`, idsJSON(ids)); err != nil {
		return nil, fmt.Errorf("delete old runs for %s: %w", task.Name, err)
	}

	return finalRuns, nil
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns() (int64, error) {
	now := time.Now()
	return db.q.MarkCrashedRuns(context.Background(), &now)
}

func (db *SQLiteDatabase) GetPendingRuns() ([]model.Run, error) {
	rows, err := db.q.GetPendingRuns(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]model.Run, 0, len(rows))
	for _, r := range rows {
		out = append(out, runFromRow(r))
	}
	return out, nil
}

func (db *SQLiteDatabase) GetLastRunByTask(taskName string) (*model.Run, error) {
	row, err := db.q.GetLastRunByTask(context.Background(), taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return runPtrFromRow(row), nil
}

func (db *SQLiteDatabase) EnsureTaskRegistered(taskName string, firstSeen time.Time) error {
	return db.q.EnsureTaskRegistered(context.Background(), sqlcdb.EnsureTaskRegisteredParams{
		TaskName:    taskName,
		FirstSeenAt: firstSeen,
	})
}

func (db *SQLiteDatabase) GetTaskRegistration(taskName string) (*model.TaskRegistration, error) {
	r, err := db.q.GetTaskRegistration(context.Background(), taskName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &model.TaskRegistration{TaskName: r.TaskName, FirstSeenAt: r.FirstSeenAt}, nil
}

func (db *SQLiteDatabase) GetConfigValue(key string) (string, bool, error) {
	val, err := db.q.GetConfigValue(context.Background(), key)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (db *SQLiteDatabase) SetConfigValue(key, value string) error {
	return db.q.SetConfigValue(context.Background(), sqlcdb.SetConfigValueParams{Key: key, Value: value})
}

func (db *SQLiteDatabase) Close() error {
	return db.db.Close()
}

func (db *SQLiteDatabase) UpsertPendingLogUpload(rec model.PendingLogUpload) error {
	return db.q.UpsertPendingLogUpload(context.Background(), sqlcdb.UpsertPendingLogUploadParams{
		ExternalExecutionID: rec.ExternalExecutionID,
		UploadUrl:           rec.UploadURL,
		LogPath:             rec.LogPath,
		InsertedAt:          rec.InsertedAt,
	})
}

func (db *SQLiteDatabase) DeletePendingLogUpload(externalExecutionID string) error {
	return db.q.DeletePendingLogUpload(context.Background(), externalExecutionID)
}

func (db *SQLiteDatabase) ListPendingLogUploads() ([]model.PendingLogUpload, error) {
	rows, err := db.q.ListPendingLogUploads(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]model.PendingLogUpload, 0, len(rows))
	for _, r := range rows {
		out = append(out, pendingLogUploadFromRow(r))
	}
	return out, nil
}
