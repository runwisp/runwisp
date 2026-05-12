// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
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
	QueryRuns(taskName string, limit, offset int, status, sortField, sortDirection, searchQuery string) ([]model.Run, error)
	DeleteRun(id string) error
	DeleteOldRuns(task *model.Task) ([]model.Run, error)
	MarkCrashedRuns() (int64, error)
	GetPendingRuns() ([]model.Run, error)
	GetLastRunByTask(taskName string) (*model.Run, error)
	GetRunSummary() (*model.RunSummary, error)
	EnsureTaskRegistered(taskName string, firstSeen time.Time) error
	GetTaskRegistration(taskName string) (*model.TaskRegistration, error)
	Close() error
}

// SQLiteDatabase wraps persistence concerns for runs and configuration.
type SQLiteDatabase struct {
	db     *sql.DB
	cipher *secretcipher.Cipher
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
instance_index        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_runs_external_execution_id ON runs(external_execution_id);
CREATE INDEX IF NOT EXISTS idx_runs_task_name ON runs(task_name);

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
// cipher, when non-nil, transparently AES-GCM-encrypts the values of the
// keys listed in SecretKeys when written via SecretStore and decrypts them on
// read. Pass nil for plaintext-default mode.
func New(dbPath string, logOutput io.Writer, cipher *secretcipher.Cipher) (Database, error) {
	_ = logOutput
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	onDisk := !strings.HasPrefix(dbPath, ":") && !strings.HasPrefix(dbPath, "file:")

	// Tighten file perms on the DB itself before any pragmas run. The umask
	// set in main covers fresh writes; this catches files left behind by
	// older binaries that may predate the umask tightening. In-memory and
	// URI-style paths (":memory:", "file::memory:") have no on-disk file —
	// skip the chmod.
	if onDisk {
		if chmodErr := os.Chmod(dbPath, 0600); chmodErr != nil && !os.IsNotExist(chmodErr) {
			return nil, fmt.Errorf("failed to chmod database: %w", chmodErr)
		}
	}

	// SQLite uses single-writer serialized mode, limit to 1 connection.
	db.SetMaxOpenConns(SQLiteMaxOpenConns)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=" + strconv.Itoa(SQLiteBusyTimeout) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	// Now that WAL mode is on (which materialises -wal/-shm next to the DB)
	// tighten the sidecar perms too. Sidecars are created lazily on the
	// first write, so chmodIfExists ignores ENOENT.
	if onDisk {
		for _, suffix := range []string{"-wal", "-shm"} {
			if chmodErr := chmodIfExists(dbPath+suffix, 0600); chmodErr != nil {
				return nil, fmt.Errorf("failed to chmod %s sidecar: %w", suffix, chmodErr)
			}
		}
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	if err := migrateAddInstanceIndex(db); err != nil {
		return nil, fmt.Errorf("failed to migrate instance_index: %w", err)
	}

	sdb := &SQLiteDatabase{db: db, cipher: cipher}
	if err := sdb.reconcileSecretEncryption(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return sdb, nil
}

// chmodIfExists tightens path to mode, treating absent files as a no-op.
// Used for the SQLite WAL/shm sidecars which SQLite creates lazily on the
// first write — they may legitimately not exist yet at boot.
func chmodIfExists(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// reconcileSecretEncryption verifies that the on-disk encryption state of
// every secret-bearing config row matches the configured cipher mode:
//
//   - If cipher is set and a secret row is plaintext, encrypt in place
//     inside one BEGIN IMMEDIATE transaction (operator just enabled Tier 2).
//   - If cipher is nil and any secret row is encrypted, refuse to start —
//     the data dir was encrypted by a previous boot; the operator forgot
//     RUNWISP_DATA_KEY and we must not silently strip protection.
//   - If cipher is set but decryption fails on any secret row, refuse to
//     start — the operator likely supplied the wrong key.
func (db *SQLiteDatabase) reconcileSecretEncryption() error {
	if db.cipher == nil {
		// Refuse to start if any secret row is encrypted but we have no key.
		for key := range SecretKeys {
			val, found, err := db.GetConfigValue(key)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			if secretcipher.IsEncrypted(val) {
				return fmt.Errorf("config row %q is encrypted at rest but %s is not set — set the key or remove the row", key, secretcipher.DataKeyEnv)
			}
		}
		return nil
	}

	tx, err := db.db.Begin()
	if err != nil {
		return fmt.Errorf("begin secret reconcile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for key := range SecretKeys {
		var val string
		err := tx.QueryRow(`SELECT value FROM config_entries WHERE key = ?`, key).Scan(&val)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read %q: %w", key, err)
		}

		if secretcipher.IsEncrypted(val) {
			// Verify the configured key actually decrypts it. Surfacing the
			// failure here gives the operator a clear message instead of an
			// opaque error later from a consumer.
			if _, err := db.cipher.Decrypt(val); err != nil {
				return fmt.Errorf("config row %q: %w", key, err)
			}
			continue
		}

		// Plaintext row + cipher present → encrypt in place.
		enc, err := db.cipher.Encrypt([]byte(val))
		if err != nil {
			return fmt.Errorf("encrypt %q: %w", key, err)
		}
		if _, err := tx.Exec(`UPDATE config_entries SET value = ? WHERE key = ?`, enc, key); err != nil {
			return fmt.Errorf("rewrite %q: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit secret reconcile tx: %w", err)
	}
	return nil
}

// migrateAddInstanceIndex is idempotent and works on three shapes of the
// runs table:
//   - Fresh install: the CREATE TABLE above already added instance_index, so
//     both ALTERs below fail with "duplicate column" / "no such column" and
//     we return nil.
//   - Pre-rename install (old daemon left a replica_index column): we rename
//     it to instance_index in place.
//   - Pre-column install (very old daemon, neither column present): we ADD
//     instance_index with a zero default.
func migrateAddInstanceIndex(db *sql.DB) error {
	if _, err := db.Exec(`ALTER TABLE runs RENAME COLUMN replica_index TO instance_index`); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "no such column") {
		return err
	}
	_, err := db.Exec(`ALTER TABLE runs ADD COLUMN instance_index INTEGER NOT NULL DEFAULT 0`)
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

func (db *SQLiteDatabase) GetRun(id string) (*model.Run, error) {
	return db.getRunWhere("id = ?", id)
}

func (db *SQLiteDatabase) GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error) {
	return db.getRunWhere("external_execution_id = ?", externalExecutionID)
}

func (db *SQLiteDatabase) getRunWhere(whereClause string, args ...any) (*model.Run, error) {
	row := db.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE `+whereClause+` LIMIT 1`, args...)
	run, err := scanRun(row)
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
FROM runs`)
	summary := &model.RunSummary{}
	if err := row.Scan(&summary.Total, &summary.Success, &summary.Failed, &summary.LastFailure); err != nil {
		return nil, err
	}
	return summary, nil
}

func (db *SQLiteDatabase) CountRuns(taskName string) (int64, error) {
	var count int64
	err := db.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE task_name = ?`, taskName).Scan(&count)
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
	return " WHERE " + strings.Join(q.where, " AND ")
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
	var q queryBuilder
	applyStatusFilter(&q, status)
	if taskName != "" {
		q.add("task_name = ?", taskName)
	}
	applySearchFilter(&q, searchQuery)

	var count int64
	err := db.db.QueryRow(`SELECT COUNT(*) FROM runs`+q.whereSQL(), q.args...).Scan(&count)
	return count, err
}

func (db *SQLiteDatabase) QueryRuns(taskName string, limit, offset int, status, sortField, sortDirection, searchQuery string) ([]model.Run, error) {
	var q queryBuilder
	if taskName != "" {
		q.add("task_name = ?", taskName)
	}
	applyStatusFilter(&q, status)
	applySearchFilter(&q, searchQuery)

	order := buildOrderClause(sortField, sortDirection)
	args := append(q.args, limit, offset)

	rows, err := db.db.Query(
		`SELECT `+runColumns+` FROM runs`+q.whereSQL()+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []model.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *r)
	}
	return runs, rows.Err()
}

func (db *SQLiteDatabase) DeleteRun(id string) error {
	_, err := db.db.Exec(`DELETE FROM runs WHERE id = ?`, id)
	return err
}

func (db *SQLiteDatabase) DeleteOldRuns(task *model.Task) ([]model.Run, error) {
	uniqueRuns := make(map[string]model.Run)

	if task.KeepFor > 0 {
		cutoff := time.Now().Add(-task.KeepFor)
		if err := db.collectRuns(uniqueRuns,
			`SELECT `+runColumns+` FROM runs WHERE task_name = ? AND created_at < ? LIMIT ?`,
			task.Name, cutoff, RetentionBatchSize,
		); err != nil {
			return nil, fmt.Errorf("query retention days for %s: %w", task.Name, err)
		}
	}

	if len(uniqueRuns) < RetentionBatchSize && task.KeepRuns > 0 {
		remaining := RetentionBatchSize - len(uniqueRuns)
		if err := db.collectRuns(uniqueRuns,
			`SELECT `+runColumns+` FROM runs WHERE task_name = ? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
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
 WHERE status = ? AND end_at IS NULL`,
		model.PhaseEnded, string(model.ReasonCrashed), now, -2, model.PhaseRunning,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (db *SQLiteDatabase) GetPendingRuns() ([]model.Run, error) {
	rows, err := db.db.Query(
		`SELECT `+runColumns+` FROM runs WHERE status = ? ORDER BY created_at ASC`,
		model.PhasePending,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []model.Run
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *r)
	}
	return runs, rows.Err()
}

func (db *SQLiteDatabase) GetLastRunByTask(taskName string) (*model.Run, error) {
	row := db.db.QueryRow(
		`SELECT `+runColumns+` FROM runs WHERE task_name = ? ORDER BY created_at DESC LIMIT 1`,
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

// DeleteConfigValue removes the row for key. Absent rows are silently ignored.
func (db *SQLiteDatabase) DeleteConfigValue(key string) error {
	_, err := db.db.Exec(`DELETE FROM config_entries WHERE key = ?`, key)
	return err
}

// GetSecret returns the (decrypted) value for key. For secret keys with a
// cipher configured, the on-disk value is AES-GCM-decrypted before return.
// Non-secret keys are returned verbatim.
func (db *SQLiteDatabase) GetSecret(key string) (string, bool, error) {
	raw, found, err := db.GetConfigValue(key)
	if err != nil || !found {
		return "", found, err
	}
	if !IsSecretKey(key) {
		return raw, true, nil
	}
	if db.cipher == nil {
		if secretcipher.IsEncrypted(raw) {
			return "", false, fmt.Errorf("config row %q is encrypted but %s is not set", key, secretcipher.DataKeyEnv)
		}
		return raw, true, nil
	}
	if !secretcipher.IsEncrypted(raw) {
		// Row predates cipher activation and reconcileSecretEncryption
		// hasn't run yet, or this is a fresh row whose write went through
		// the legacy SetConfigValue. Treat as a plaintext value.
		return raw, true, nil
	}
	pt, err := db.cipher.Decrypt(raw)
	if err != nil {
		return "", false, fmt.Errorf("decrypt %q: %w", key, err)
	}
	return string(pt), true, nil
}

// SetSecret persists key=value. For secret keys with a cipher configured,
// the value is AES-GCM-encrypted before storage so the on-disk row carries
// the "enc:v1:" prefix. Non-secret keys go through the regular config write.
func (db *SQLiteDatabase) SetSecret(key, value string) error {
	if !IsSecretKey(key) || db.cipher == nil {
		return db.SetConfigValue(key, value)
	}
	enc, err := db.cipher.Encrypt([]byte(value))
	if err != nil {
		return fmt.Errorf("encrypt %q: %w", key, err)
	}
	return db.SetConfigValue(key, enc)
}

func (db *SQLiteDatabase) Close() error {
	return db.db.Close()
}

func (db *SQLiteDatabase) UpsertPendingLogUpload(rec PendingLogUpload) error {
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

func (db *SQLiteDatabase) ListPendingLogUploads() ([]PendingLogUpload, error) {
	rows, err := db.db.Query(
		`SELECT external_execution_id, upload_url, log_path, inserted_at FROM pending_log_uploads`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recs []PendingLogUpload
	for rows.Next() {
		var r PendingLogUpload
		if err := rows.Scan(&r.ExternalExecutionID, &r.UploadURL, &r.LogPath, &r.InsertedAt); err != nil {
			return nil, err
		}
		recs = append(recs, r)
	}
	return recs, rows.Err()
}

func buildOrderClause(sortField, sortDirection string) string {
	if sortField == "" {
		return "created_at DESC"
	}

	direction := "DESC"
	if sortDirection == "asc" {
		direction = "ASC"
	}

	switch sortField {
	case "duration":
		return fmt.Sprintf("(COALESCE(julianday(end_at) - julianday(start_at), 0)) %s", direction)
	case "start_at":
		return fmt.Sprintf("COALESCE(start_at, created_at) %s, created_at %s", direction, direction)
	case "task_name", "status", "exit_code", "created_at":
		return fmt.Sprintf("%s %s", sortField, direction)
	default:
		return "created_at DESC"
	}
}
