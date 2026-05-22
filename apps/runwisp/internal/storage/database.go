// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the SQLite driver for database/sql

	"github.com/runwisp/runwisp/internal/model"
)

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

// SQLiteDatabase wraps persistence concerns for runs and configuration.
type SQLiteDatabase struct {
	db *sql.DB
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS runs (
id                    TEXT PRIMARY KEY,
external_execution_id TEXT,
task_name             TEXT NOT NULL DEFAULT '',
status                VARCHAR(20) NOT NULL,
end_reason            VARCHAR(20),
exit_code             INTEGER DEFAULT 0,
start_at              DATETIME,
end_at                DATETIME,
triggered_by          VARCHAR(20) NOT NULL,
created_at            DATETIME,
retry_attempt         INTEGER DEFAULT 0,
retry_of_run_id       TEXT,
instance_index        INTEGER NOT NULL DEFAULT 0,
deleted_at            DATETIME
);
CREATE INDEX IF NOT EXISTS idx_runs_external_execution_id ON runs(external_execution_id);
CREATE INDEX IF NOT EXISTS idx_runs_task_name ON runs(task_name);
CREATE INDEX IF NOT EXISTS idx_runs_deleted_at ON runs(deleted_at);
CREATE INDEX IF NOT EXISTS idx_runs_created_at_desc ON runs(created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS task_registrations (
task_name     TEXT PRIMARY KEY,
first_seen_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS config_entries (
key   TEXT PRIMARY KEY,
value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
id                TEXT PRIMARY KEY,
fingerprint       TEXT NOT NULL,
kind              TEXT NOT NULL,
severity          TEXT NOT NULL,
task_name         TEXT NOT NULL DEFAULT '',
run_id            TEXT NOT NULL DEFAULT '',
title             TEXT NOT NULL DEFAULT '',
body              TEXT NOT NULL DEFAULT '',
count             INTEGER NOT NULL DEFAULT 1,
occurrences_json  TEXT NOT NULL DEFAULT '[]',
created_at        DATETIME NOT NULL,
last_occurred_at  DATETIME NOT NULL,
read_at           DATETIME
);
CREATE INDEX IF NOT EXISTS idx_notifications_fingerprint ON notifications(fingerprint);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_severity ON notifications(severity);
CREATE INDEX IF NOT EXISTS idx_notifications_read_at ON notifications(read_at);

CREATE TABLE IF NOT EXISTS pending_log_uploads (
external_execution_id TEXT PRIMARY KEY,
upload_url            TEXT NOT NULL,
log_path              TEXT NOT NULL,
inserted_at           INTEGER NOT NULL
);
`

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

	if err := migrateAddDeletedAt(db); err != nil {
		return nil, fmt.Errorf("failed to migrate deleted_at: %w", err)
	}

	return &SQLiteDatabase{db: db}, nil
}

// migrateAddDeletedAt adds the deleted_at column to runs on legacy databases.
// Idempotent: the CREATE TABLE above already includes the column on fresh
// installs, so the ALTER fails with "duplicate column" and we return nil.
func migrateAddDeletedAt(db *sql.DB) error {
	_, err := db.Exec(`ALTER TABLE runs ADD COLUMN deleted_at DATETIME`)
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "duplicate column") {
		return nil
	}
	return err
}

const runColumns = `id, external_execution_id, task_name, status, end_reason, exit_code,
start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index`

func (db *SQLiteDatabase) CreateRun(run *model.Run) error {
	_, err := db.db.Exec(
		`INSERT INTO runs (`+runColumns+`)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.ExternalExecutionID, run.TaskName, run.Status, run.EndReason, run.ExitCode,
		run.StartAt, run.EndAt, run.TriggeredBy, run.CreatedAt, run.RetryAttempt, run.RetryOfRunID, run.InstanceIndex,
	)
	return err
}

func (db *SQLiteDatabase) UpdateRun(run *model.Run) error {
	_, err := db.db.Exec(
		`UPDATE runs SET
external_execution_id = ?, task_name = ?, status = ?, end_reason = ?, exit_code = ?,
start_at = ?, end_at = ?, triggered_by = ?, created_at = ?,
retry_attempt = ?, retry_of_run_id = ?, instance_index = ?
 WHERE id = ?`,
		run.ExternalExecutionID, run.TaskName, run.Status, run.EndReason, run.ExitCode,
		run.StartAt, run.EndAt, run.TriggeredBy, run.CreatedAt,
		run.RetryAttempt, run.RetryOfRunID, run.InstanceIndex,
		run.ID,
	)
	return err
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
	return db.scanSingleRun(selectRunByIDSQL, id)
}

func (db *SQLiteDatabase) GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error) {
	return db.scanSingleRun(selectRunByExternalIDSQL, externalExecutionID)
}

func (db *SQLiteDatabase) scanSingleRun(query string, args ...any) (*model.Run, error) {
	run, err := scanRun(db.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return run, err
}

func (db *SQLiteDatabase) GetRunSummary() (*model.RunSummary, error) {
	row := db.db.QueryRow(`
SELECT COUNT(*),
COALESCE(SUM(CASE WHEN end_reason = 'success' THEN 1 ELSE 0 END), 0),
COALESCE(SUM(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow') THEN 1 ELSE 0 END), 0),
MAX(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow') THEN end_at END)
FROM runs WHERE deleted_at IS NULL`)
	summary := &model.RunSummary{}
	if err := row.Scan(&summary.Total, &summary.Success, &summary.Failed, &summary.LastFailure); err != nil {
		return nil, err
	}
	return summary, nil
}

func (db *SQLiteDatabase) CountRuns(taskName string) (int64, error) {
	var count int64
	err := db.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_name = ? AND deleted_at IS NULL`, taskName).Scan(&count)
	return count, err
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
	_, err := db.db.Exec(`DELETE FROM runs WHERE id = ?`, id)
	return err
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
		// Re-read with the same predicate (deleted_at is now NULL so the
		// rows are visible to the standard SELECT) so we can publish full
		// updates.
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
	rows, err := db.db.Query(
		`DELETE FROM runs WHERE deleted_at IS NOT NULL AND deleted_at <= ?
 RETURNING id, task_name, created_at`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	return scanRunRefs(rows)
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

	if task.KeepFor > 0 {
		cutoff := time.Now().Add(-task.KeepFor)
		if err := db.collectRuns(uniqueRuns,
			selectRunsSQL("WHERE task_name = ? AND created_at < ?", "LIMIT ?"),
			task.Name, cutoff, RetentionBatchSize,
		); err != nil {
			return nil, fmt.Errorf("query retention days for %s: %w", task.Name, err)
		}
	}

	if len(uniqueRuns) < RetentionBatchSize && task.KeepRuns > 0 {
		remaining := RetentionBatchSize - len(uniqueRuns)
		if err := db.collectRuns(uniqueRuns,
			selectRunsSQL("WHERE task_name = ?", "ORDER BY created_at DESC LIMIT ? OFFSET ?"),
			task.Name, remaining, task.KeepRuns,
		); err != nil {
			return nil, fmt.Errorf("query retention runs for %s: %w", task.Name, err)
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

func (db *SQLiteDatabase) collectRuns(into map[string]model.Run, query string, args ...any) error {
	rows, err := db.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return err
		}
		into[r.ID] = *r
	}
	return rows.Err()
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns() (int64, error) {
	now := time.Now()
	result, err := db.db.Exec(
		`UPDATE runs SET status = ?, end_reason = ?, end_at = ?, exit_code = ?
 WHERE status = ? AND end_at IS NULL AND deleted_at IS NULL`,
		model.PhaseEnded, string(model.ReasonCrashed), now, -2, model.PhaseRunning,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (db *SQLiteDatabase) GetPendingRuns() ([]model.Run, error) {
	rows, err := db.db.Query(
		selectRunsSQL("WHERE status = ?", "ORDER BY created_at ASC"),
		model.PhasePending,
	)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanRun)
}

func (db *SQLiteDatabase) GetLastRunByTask(taskName string) (*model.Run, error) {
	row := db.db.QueryRow(
		selectRunsSQL("WHERE task_name = ?", "ORDER BY created_at DESC LIMIT 1"),
		taskName,
	)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return run, err
}

func (db *SQLiteDatabase) EnsureTaskRegistered(taskName string, firstSeen time.Time) error {
	_, err := db.db.Exec(
		`INSERT OR IGNORE INTO task_registrations (task_name, first_seen_at) VALUES (?, ?)`,
		taskName, firstSeen,
	)
	return err
}

func (db *SQLiteDatabase) GetTaskRegistration(taskName string) (*model.TaskRegistration, error) {
	var r model.TaskRegistration
	err := db.db.QueryRow(
		`SELECT task_name, first_seen_at FROM task_registrations WHERE task_name = ?`,
		taskName,
	).Scan(&r.TaskName, &r.FirstSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (db *SQLiteDatabase) GetConfigValue(key string) (string, bool, error) {
	var value string
	err := db.db.QueryRow(`SELECT value FROM config_entries WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (db *SQLiteDatabase) SetConfigValue(key, value string) error {
	_, err := db.db.Exec(
		`INSERT INTO config_entries (key, value) VALUES (?, ?)
 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (db *SQLiteDatabase) Close() error {
	return db.db.Close()
}

func (db *SQLiteDatabase) UpsertPendingLogUpload(rec model.PendingLogUpload) error {
	_, err := db.db.Exec(
		`INSERT INTO pending_log_uploads (external_execution_id, upload_url, log_path, inserted_at)
 VALUES (?, ?, ?, ?)
 ON CONFLICT(external_execution_id) DO UPDATE SET
   upload_url = excluded.upload_url,
   log_path = excluded.log_path,
   inserted_at = excluded.inserted_at`,
		rec.ExternalExecutionID, rec.UploadURL, rec.LogPath, rec.InsertedAt,
	)
	return err
}

func (db *SQLiteDatabase) DeletePendingLogUpload(externalExecutionID string) error {
	_, err := db.db.Exec(
		`DELETE FROM pending_log_uploads WHERE external_execution_id = ?`,
		externalExecutionID,
	)
	return err
}

func (db *SQLiteDatabase) ListPendingLogUploads() ([]model.PendingLogUpload, error) {
	rows, err := db.db.Query(
		`SELECT external_execution_id, upload_url, log_path, inserted_at FROM pending_log_uploads`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []model.PendingLogUpload
	for rows.Next() {
		var r model.PendingLogUpload
		if err := rows.Scan(&r.ExternalExecutionID, &r.UploadURL, &r.LogPath, &r.InsertedAt); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}
