// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/glebarez/sqlite"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/xhit/go-str2duration/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	MaxSearchQueryLength = 100  // Max characters in search query
	RetentionBatchSize   = 1000 // Max runs to delete per batch
	SQLiteBusyTimeout    = 5000 // SQLite busy timeout in ms
	SQLiteMaxOpenConns   = 1    // SQLite uses single-writer serialized mode
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
	// EnsureTaskRegistered records the first time a task was seen by the daemon.
	// Uses INSERT OR IGNORE so subsequent calls are no-ops, preserving the original timestamp.
	EnsureTaskRegistered(taskName string, firstSeen time.Time) error
	// GetTaskRegistration returns the per-task registration record, or nil if none exists.
	GetTaskRegistration(taskName string) (*model.TaskRegistration, error)
	Close() error
}

// SQLiteDatabase wraps persistence concerns for runs.
type SQLiteDatabase struct {
	db *gorm.DB
}

// New opens (and migrates) the SQLite database.
// logOutput controls where GORM logs are written; nil defaults to os.Stderr.
func New(dbPath string, logOutput io.Writer) (Database, error) {
	if logOutput == nil {
		logOutput = os.Stderr
	}
	gormLogger := logger.New(
		stdlog.New(logOutput, "", stdlog.LstdFlags),
		logger.Config{
			SlowThreshold:             time.Second,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)

	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.AutoMigrate(&model.Run{}, &model.TaskRegistration{}, &model.ConfigEntry{}); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	// Optimize SQLite for concurrency
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		return nil, fmt.Errorf("failed to enable WAL mode: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA busy_timeout=" + strconv.Itoa(SQLiteBusyTimeout) + ";"); err != nil {
		return nil, fmt.Errorf("failed to set busy timeout: %w", err)
	}

	// SQLite uses single-writer serialized mode, limit to 1 connection
	sqlDB.SetMaxOpenConns(SQLiteMaxOpenConns)

	return &SQLiteDatabase{db: db}, nil
}

func (db *SQLiteDatabase) CreateRun(run *model.Run) error {
	return db.db.Create(run).Error
}

func (db *SQLiteDatabase) UpdateRun(run *model.Run) error {
	return db.db.Save(run).Error
}

func (db *SQLiteDatabase) GetRun(id string) (*model.Run, error) {
	return db.getRunWhere("id = ?", id)
}

func (db *SQLiteDatabase) GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error) {
	return db.getRunWhere("external_execution_id = ?", externalExecutionID)
}

func (db *SQLiteDatabase) getRunWhere(query string, args ...any) (*model.Run, error) {
	var run model.Run
	if err := db.db.Where(query, args...).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &run, nil
}

func (db *SQLiteDatabase) GetRunSummary() (*model.RunSummary, error) {
	var result struct {
		Total       int64
		Success     int64
		Failed      int64
		LastFailure *time.Time
	}
	err := db.db.Model(&model.Run{}).
		Select(
			"COUNT(*) AS total",
			"SUM(CASE WHEN end_reason = 'success' THEN 1 ELSE 0 END) AS success",
			"SUM(CASE WHEN end_reason IN ('failed','crashed','timeout') THEN 1 ELSE 0 END) AS failed",
			"MAX(CASE WHEN end_reason IN ('failed','crashed','timeout') THEN end_at END) AS last_failure",
		).
		Scan(&result).Error
	if err != nil {
		return nil, err
	}
	return &model.RunSummary{
		Total:       result.Total,
		Success:     result.Success,
		Failed:      result.Failed,
		LastFailure: result.LastFailure,
	}, nil
}

func (db *SQLiteDatabase) CountRuns(taskName string) (int64, error) {
	var count int64
	err := db.db.Model(&model.Run{}).Where("task_name = ?", taskName).Count(&count).Error
	return count, err
}

func (db *SQLiteDatabase) CountRunsFiltered(status, taskName, searchQuery string) (int64, error) {
	var count int64
	query := db.db.Model(&model.Run{})

	query = applyStatusFilter(query, status)
	if taskName != "" {
		query = query.Where("task_name = ?", taskName)
	}

	query = db.applySearchFilter(query, searchQuery)

	err := query.Count(&count).Error
	return count, err
}

func (db *SQLiteDatabase) QueryRuns(taskName string, limit, offset int, status, sortField, sortDirection, searchQuery string) ([]model.Run, error) {
	var runs []model.Run
	query := db.db.Model(&model.Run{})
	if taskName != "" {
		query = query.Where("task_name = ?", taskName)
	}
	query = applyStatusFilter(query, status)
	query = db.applySearchFilter(query, searchQuery)
	return runs, query.Order(db.buildOrderClause(sortField, sortDirection)).Limit(limit).Offset(offset).Find(&runs).Error
}

func (db *SQLiteDatabase) DeleteRun(id string) error {
	return db.db.Delete(&model.Run{}, "id = ?", id).Error
}

func (db *SQLiteDatabase) DeleteOldRuns(task *model.Task) ([]model.Run, error) {
	uniqueRuns := make(map[string]model.Run)

	if task.Retention.Age != "" {
		parsedInterval, err := str2duration.ParseDuration(task.Retention.Age)
		if err != nil {
			log.Warn("Invalid retention age", "age", task.Retention.Age, "task", task.Name, "err", err)
		} else {
			cutoff := time.Now().Add(-parsedInterval)
			var runs []model.Run
			if err := db.db.Where("task_name = ? AND created_at < ?", task.Name, cutoff).Limit(RetentionBatchSize).Find(&runs).Error; err != nil {
				return nil, fmt.Errorf("query retention days for %s: %w", task.Name, err)
			}
			for _, r := range runs {
				uniqueRuns[r.ID] = r
			}
		}
	}

	if len(uniqueRuns) < RetentionBatchSize && task.Retention.Runs > 0 {
		var runs []model.Run
		remaining := RetentionBatchSize - len(uniqueRuns)
		if err := db.db.Where("task_name = ?", task.Name).Order("created_at DESC").Offset(task.Retention.Runs).Limit(remaining).Find(&runs).Error; err != nil {
			return nil, fmt.Errorf("query retention runs for %s: %w", task.Name, err)
		}
		for _, r := range runs {
			uniqueRuns[r.ID] = r
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

	if err := db.db.Delete(&model.Run{}, "id IN ?", ids).Error; err != nil {
		return nil, fmt.Errorf("delete old runs for %s: %w", task.Name, err)
	}

	return finalRuns, nil
}

// MarkCrashedRuns flags runs that never completed (e.g., after a crash).
func (db *SQLiteDatabase) MarkCrashedRuns() (int64, error) {
	now := time.Now()
	crashed := model.ReasonCrashed
	// Only mark RUNNING tasks as crashed. PENDING tasks should be resumed.
	result := db.db.Model(&model.Run{}).
		Where("status = ? AND end_at IS NULL", model.PhaseRunning).
		Updates(map[string]any{
			"status":     model.PhaseEnded,
			"end_reason": crashed,
			"end_at":     now,
			"exit_code":  -2,
		})

	if result.Error != nil {
		return 0, result.Error
	}

	return result.RowsAffected, nil
}

func (db *SQLiteDatabase) GetPendingRuns() ([]model.Run, error) {
	var runs []model.Run
	err := db.db.Where("status = ?", model.PhasePending).Order("created_at ASC").Find(&runs).Error
	return runs, err
}

func (db *SQLiteDatabase) GetLastRunByTask(taskName string) (*model.Run, error) {
	var run model.Run
	err := db.db.Where("task_name = ?", taskName).Order("created_at DESC").First(&run).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (db *SQLiteDatabase) EnsureTaskRegistered(taskName string, firstSeen time.Time) error {
	return db.db.Exec(
		"INSERT OR IGNORE INTO task_registrations (task_name, first_seen_at) VALUES (?, ?)",
		taskName, firstSeen,
	).Error
}

func (db *SQLiteDatabase) GetTaskRegistration(taskName string) (*model.TaskRegistration, error) {
	var r model.TaskRegistration
	err := db.db.First(&r, "task_name = ?", taskName).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

func (db *SQLiteDatabase) GetConfigValue(key string) (string, bool, error) {
	var entry model.ConfigEntry
	err := db.db.First(&entry, "key = ?", key).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return entry.Value, true, nil
}

func (db *SQLiteDatabase) SetConfigValue(key, value string) error {
	return db.db.Exec(
		"INSERT INTO config_entries (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value,
	).Error
}

func (db *SQLiteDatabase) Close() error {
	sqlDB, err := db.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (db *SQLiteDatabase) applySearchFilter(query *gorm.DB, searchQuery string) *gorm.DB {
	if searchQuery == "" {
		return query
	}

	// Prevent excessively long search queries
	if len(searchQuery) > MaxSearchQueryLength {
		searchQuery = searchQuery[:MaxSearchQueryLength]
	}

	// Sanitize LIKE wildcard characters to prevent injection
	searchQuery = strings.ReplaceAll(searchQuery, "%", "")
	searchQuery = strings.ReplaceAll(searchQuery, "_", "")

	searchPattern := "%" + searchQuery + "%"
	return query.Where("task_name LIKE ? OR id LIKE ?", searchPattern, searchPattern)
}

func (db *SQLiteDatabase) buildOrderClause(sortField, sortDirection string) string {
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
		coalesced := fmt.Sprintf("COALESCE(%s, created_at)", sortField)
		return fmt.Sprintf("%s %s, created_at %s", coalesced, direction, direction)
	case "task_name", "status", "exit_code", "created_at":
		return fmt.Sprintf("%s %s", sortField, direction)
	default:
		return "created_at DESC"
	}
}

// applyStatusFilter adds a WHERE clause that matches either a phase or an
// end_reason depending on the supplied value.
func applyStatusFilter(query *gorm.DB, status string) *gorm.DB {
	if status == "" {
		return query
	}
	switch model.EndReason(status) {
	case model.ReasonSuccess, model.ReasonFailed, model.ReasonStopped, model.ReasonTimeout, model.ReasonCrashed:
		return query.Where("end_reason = ?", status)
	default:
		return query.Where("status = ?", status)
	}
}
