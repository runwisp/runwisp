// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Notification is one persistent in-app notification row, possibly representing
// many coalesced occurrences of the same underlying event.
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
}

// NotificationRepository persists in-app notifications and read-state.
type NotificationRepository interface {
	// UpsertByFingerprint folds an event into an existing row when one matches
	// the fingerprint and falls within the coalescing window; otherwise inserts.
	// ringSize trims the merged occurrence list (0 = no trim).
	UpsertByFingerprint(n *Notification, window time.Duration, ringSize int) (created bool, err error)
	UpdateOccurrence(id string, count int, lastOccurredAt time.Time, occurrences []time.Time, title, body string) error
	ListNotifications(limit int, before string) ([]Notification, error)
	GetNotificationByID(id string) (*Notification, error)
	PruneNotificationsByCount(keep int) (int64, error)
	PruneNotificationsByAge(olderThan time.Duration) (int64, error)
	CountNotificationsSince(t time.Time) (int64, error)
	GetLastReadAt() (time.Time, error)
	SetLastReadAt(t time.Time) error
}

const notificationColumns = `id, fingerprint, kind, severity, task_name, run_id, title, body,
count, occurrences_json, created_at, last_occurred_at`

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
	if err := scanner.Scan(
		&n.ID, &n.Fingerprint, &n.Kind, &n.Severity, &n.TaskName, &n.RunID,
		&n.Title, &n.Body, &n.Count, &occJSON, &n.CreatedAt, &n.LastOccurredAt,
	); err != nil {
		return nil, err
	}
	occ, err := decodeOccurrences(occJSON)
	if err != nil {
		return nil, fmt.Errorf("decode occurrences for %s: %w", n.ID, err)
	}
	n.Occurrences = occ
	return &n, nil
}

// UpsertByFingerprint folds new occurrences into an existing row when one
// matches the fingerprint and falls within the coalescing window. Otherwise
// it inserts the supplied notification as a fresh row. The decision and the
// write happen in a single transaction.
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
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			n.ID, n.Fingerprint, n.Kind, n.Severity, n.TaskName, n.RunID,
			n.Title, n.Body, n.Count, occJSON, n.CreatedAt, n.LastOccurredAt,
		); err != nil {
			return false, err
		}
		if err := tx.Commit(); err != nil {
			return false, err
		}
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
		 SET count = ?, occurrences_json = ?, last_occurred_at = ?, title = ?, body = ?
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
	return false, nil
}

// UpdateOccurrence rewrites count/last_occurred_at/occurrences/title/body for
// an existing row. Used when the in-memory coalescer decided to fold an event
// into a row it already tracks (no SELECT round-trip needed).
func (db *SQLiteDatabase) UpdateOccurrence(id string, count int, lastOccurredAt time.Time, occurrences []time.Time, title, body string) error {
	occJSON, err := encodeOccurrences(occurrences)
	if err != nil {
		return err
	}
	res, err := db.db.Exec(
		`UPDATE notifications
		 SET count = ?, occurrences_json = ?, last_occurred_at = ?, title = ?, body = ?
		 WHERE id = ?`,
		count, occJSON, lastOccurredAt, title, body, id,
	)
	if err != nil {
		return err
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
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
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *n)
	}
	return out, rows.Err()
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

// CountNotificationsSince counts rows whose last_occurred_at is strictly after t.
func (db *SQLiteDatabase) CountNotificationsSince(t time.Time) (int64, error) {
	var c int64
	err := db.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE last_occurred_at > ?`, t).Scan(&c)
	return c, err
}

// GetLastReadAt returns the operator's last-read marker, or zero time if unset.
func (db *SQLiteDatabase) GetLastReadAt() (time.Time, error) {
	var t time.Time
	err := db.db.QueryRow(`SELECT last_read_at FROM notification_read_state WHERE id = 1`).Scan(&t)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return time.Time{}, nil
	case err != nil:
		// Some SQLite drivers return a parse error rather than ErrNoRows in edge
		// cases (legacy DBs without the row). Treat any decode failure as "unset".
		if strings.Contains(err.Error(), "parsing time") {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return t, nil
}

// SetLastReadAt persists a single-row marker.
func (db *SQLiteDatabase) SetLastReadAt(t time.Time) error {
	_, err := db.db.Exec(
		`INSERT INTO notification_read_state (id, last_read_at) VALUES (1, ?)
		 ON CONFLICT(id) DO UPDATE SET last_read_at = excluded.last_read_at`,
		t,
	)
	return err
}
