// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Notification is one persistent in-app notification row, possibly representing
// many coalesced occurrences of the same underlying event. ReadAt is nil when
// the row is unread; a non-nil pointer is the moment it was marked read.
type Notification struct {
	ID             string
	Fingerprint    string
	Kind           string
	Severity       string
	TaskName       string
	RunID          string
	Title          string
	Body           string
	Count          int
	Occurrences    []time.Time
	CreatedAt      time.Time
	LastOccurredAt time.Time
	ReadAt         *time.Time
}

// NotificationRepository persists in-app notifications and per-row read state.
type NotificationRepository interface {
	// UpsertByFingerprint folds an event into an existing row when one matches
	// the fingerprint and falls within the coalescing window; otherwise inserts.
	// ringSize trims the merged occurrence list (0 = no trim). A successful
	// coalesce clears ReadAt so a fresh occurrence makes the row unread again.
	UpsertByFingerprint(n *Notification, window time.Duration, ringSize int) (created bool, err error)
	ListNotifications(limit int, before string) ([]Notification, error)
	GetNotificationByID(id string) (*Notification, error)
	PruneNotificationsByCount(keep int) (int64, error)
	PruneNotificationsByAge(olderThan time.Duration) (int64, error)
	CountUnreadNotifications() (int64, error)
	MarkNotificationRead(id string, at time.Time) (*Notification, error)
	MarkNotificationUnread(id string) (*Notification, error)
	MarkAllNotificationsRead(at time.Time) error
}

const notificationColumns = `id, fingerprint, kind, severity, task_name, run_id, title, body,
count, occurrences_json, created_at, last_occurred_at, read_at`

func encodeOccurrences(ts []time.Time) (string, error) {
	if ts == nil {
		ts = []time.Time{}
	}
	b, err := json.Marshal(ts)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeOccurrences(s string) ([]time.Time, error) {
	if s == "" {
		return nil, nil
	}
	var ts []time.Time
	if err := json.Unmarshal([]byte(s), &ts); err != nil {
		return nil, err
	}
	return ts, nil
}

func scanNotification(scanner interface {
	Scan(dest ...any) error
}) (*Notification, error) {
	var n Notification
	var occJSON string
	var readAt sql.NullTime
	if err := scanner.Scan(
		&n.ID, &n.Fingerprint, &n.Kind, &n.Severity, &n.TaskName, &n.RunID,
		&n.Title, &n.Body, &n.Count, &occJSON, &n.CreatedAt, &n.LastOccurredAt, &readAt,
	); err != nil {
		return nil, err
	}
	occ, err := decodeOccurrences(occJSON)
	if err != nil {
		return nil, fmt.Errorf("decode occurrences for %s: %w", n.ID, err)
	}
	n.Occurrences = occ
	if readAt.Valid {
		t := readAt.Time
		n.ReadAt = &t
	}
	return &n, nil
}

// UpsertByFingerprint folds new occurrences into an existing row when one
// matches the fingerprint and falls within the coalescing window. Otherwise
// it inserts the supplied notification as a fresh row. The decision and the
// write happen in a single transaction. A coalesce clears read_at — a new
// occurrence of a previously-acknowledged event surfaces it again.
func (db *SQLiteDatabase) UpsertByFingerprint(n *Notification, window time.Duration, ringSize int) (bool, error) {
	if n == nil {
		return false, errors.New("notification is nil")
	}

	tx, err := db.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	cutoff := n.LastOccurredAt.Add(-window)

	var (
		existingID          string
		existingCount       int
		existingOccurrences string
	)
	row := tx.QueryRow(
		`SELECT id, count, occurrences_json FROM notifications
		 WHERE fingerprint = ? AND last_occurred_at >= ?
		 ORDER BY last_occurred_at DESC LIMIT 1`,
		n.Fingerprint, cutoff,
	)
	switch err := row.Scan(&existingID, &existingCount, &existingOccurrences); {
	case errors.Is(err, sql.ErrNoRows):
		occJSON, err := encodeOccurrences(n.Occurrences)
		if err != nil {
			return false, err
		}
		if _, err := tx.Exec(
			`INSERT INTO notifications (`+notificationColumns+`)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
			n.ID, n.Fingerprint, n.Kind, n.Severity, n.TaskName, n.RunID,
			n.Title, n.Body, n.Count, occJSON, n.CreatedAt, n.LastOccurredAt,
		); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
		n.ReadAt = nil
		return true, nil
	case err != nil:
		return false, err
	}

	prev, err := decodeOccurrences(existingOccurrences)
	if err != nil {
		return false, fmt.Errorf("decode existing occurrences: %w", err)
	}
	merged := append([]time.Time{n.LastOccurredAt}, prev...)
	if ringSize > 0 && len(merged) > ringSize {
		merged = merged[:ringSize]
	}
	mergedJSON, err := encodeOccurrences(merged)
	if err != nil {
		return false, err
	}

	newCount := existingCount + 1
	if _, err := tx.Exec(
		`UPDATE notifications
		 SET count = ?, occurrences_json = ?, last_occurred_at = ?, title = ?, body = ?, read_at = NULL
		 WHERE id = ?`,
		newCount, mergedJSON, n.LastOccurredAt, n.Title, n.Body, existingID,
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	n.ID = existingID
	n.Count = newCount
	n.Occurrences = merged
	n.ReadAt = nil
	return false, nil
}

// ListNotifications returns the most recent notifications ordered by id DESC
// (which is monotonic for ULIDs). When before is non-empty, only rows with
// id < before are returned (cursor-based pagination).
func (db *SQLiteDatabase) ListNotifications(limit int, before string) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	args := []any{}
	clause := ""
	if before != "" {
		clause = " WHERE id < ?"
		args = append(args, before)
	}
	args = append(args, limit)

	rows, err := db.db.Query(
		`SELECT `+notificationColumns+` FROM notifications`+clause+` ORDER BY id DESC LIMIT ?`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	return collectRows(rows, scanNotification)
}

func (db *SQLiteDatabase) GetNotificationByID(id string) (*Notification, error) {
	row := db.db.QueryRow(`SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, id)
	n, err := scanNotification(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return n, err
}

// PruneNotificationsByCount keeps the most recent `keep` rows and deletes the rest.
func (db *SQLiteDatabase) PruneNotificationsByCount(keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := db.db.Exec(
		`DELETE FROM notifications WHERE id IN (
			SELECT id FROM notifications ORDER BY id DESC LIMIT -1 OFFSET ?
		)`, keep,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneNotificationsByAge deletes rows whose last_occurred_at is older than the cutoff.
func (db *SQLiteDatabase) PruneNotificationsByAge(olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan)
	res, err := db.db.Exec(`DELETE FROM notifications WHERE last_occurred_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountUnreadNotifications counts rows whose read_at is NULL.
func (db *SQLiteDatabase) CountUnreadNotifications() (int64, error) {
	var c int64
	err := db.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE read_at IS NULL`).Scan(&c)
	return c, err
}

// MarkNotificationRead stamps read_at on a single row and returns the updated
// row. Re-marking an already-read row overwrites the timestamp.
func (db *SQLiteDatabase) MarkNotificationRead(id string, at time.Time) (*Notification, error) {
	return db.setNotificationReadAt(id, &at)
}

// MarkNotificationUnread clears read_at on a single row and returns it.
func (db *SQLiteDatabase) MarkNotificationUnread(id string) (*Notification, error) {
	return db.setNotificationReadAt(id, nil)
}

func (db *SQLiteDatabase) setNotificationReadAt(id string, at *time.Time) (*Notification, error) {
	if id == "" {
		return nil, errors.New("notification id is empty")
	}
	res, err := db.db.Exec(`UPDATE notifications SET read_at = ? WHERE id = ?`, at, id)
	if err != nil {
		return nil, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return nil, ErrNotFound
	}
	return db.GetNotificationByID(id)
}

// MarkAllNotificationsRead stamps read_at on every currently-unread row.
func (db *SQLiteDatabase) MarkAllNotificationsRead(at time.Time) error {
	_, err := db.db.Exec(
		`UPDATE notifications SET read_at = ? WHERE read_at IS NULL`,
		at,
	)
	return err
}
