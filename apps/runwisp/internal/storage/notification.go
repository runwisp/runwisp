// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
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
	UpsertByFingerprint(ctx context.Context, n *Notification, window time.Duration, ringSize int) (created bool, err error)
	ListNotifications(ctx context.Context, limit int, before string) ([]Notification, error)
	GetNotificationByID(ctx context.Context, id string) (*Notification, error)
	PruneNotificationsByCount(ctx context.Context, keep int) (int64, error)
	PruneNotificationsByAge(ctx context.Context, olderThan time.Duration) (int64, error)
	CountUnreadNotifications(ctx context.Context) (int64, error)
	MarkNotificationRead(ctx context.Context, id string, at time.Time) (*Notification, error)
	MarkNotificationUnread(ctx context.Context, id string) (*Notification, error)
	MarkAllNotificationsRead(ctx context.Context, at time.Time) error
}

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

// UpsertByFingerprint folds new occurrences into an existing row when one
// matches the fingerprint and falls within the coalescing window. Otherwise
// it inserts the supplied notification as a fresh row. The decision and the
// write happen in a single transaction. A coalesce clears read_at — a new
// occurrence of a previously-acknowledged event surfaces it again.
func (db *SQLiteDatabase) UpsertByFingerprint(ctx context.Context, n *Notification, window time.Duration, ringSize int) (bool, error) {
	if n == nil {
		return false, errors.New("notification is nil")
	}

	tx, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	qtx := db.q.WithTx(tx)

	cutoff := n.LastOccurredAt.Add(-window)
	existing, err := qtx.SelectExistingForFingerprint(ctx, sqlcdb.SelectExistingForFingerprintParams{
		Fingerprint:    n.Fingerprint,
		LastOccurredAt: cutoff,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		occJSON, err := encodeOccurrences(n.Occurrences)
		if err != nil {
			return false, err
		}
		if err := qtx.InsertNotification(ctx, sqlcdb.InsertNotificationParams{
			ID:              n.ID,
			Fingerprint:     n.Fingerprint,
			Kind:            n.Kind,
			Severity:        n.Severity,
			TaskName:        n.TaskName,
			RunID:           n.RunID,
			Title:           n.Title,
			Body:            n.Body,
			Count:           n.Count,
			OccurrencesJson: occJSON,
			CreatedAt:       n.CreatedAt,
			LastOccurredAt:  n.LastOccurredAt,
		}); err != nil {
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

	prev, err := decodeOccurrences(existing.OccurrencesJson)
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

	newCount := existing.Count + 1
	if err := qtx.UpdateNotificationCoalesced(ctx, sqlcdb.UpdateNotificationCoalescedParams{
		Count:           newCount,
		OccurrencesJson: mergedJSON,
		LastOccurredAt:  n.LastOccurredAt,
		Title:           n.Title,
		Body:            n.Body,
		ID:              existing.ID,
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}

	n.ID = existing.ID
	n.Count = newCount
	n.Occurrences = merged
	n.ReadAt = nil
	return false, nil
}

// ListNotifications returns the most recent notifications ordered by id DESC
// (which is monotonic for ULIDs). When before is non-empty, only rows with
// id < before are returned (cursor-based pagination).
func (db *SQLiteDatabase) ListNotifications(ctx context.Context, limit int, before string) ([]Notification, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []sqlcdb.Notification
	var err error
	if before != "" {
		rows, err = db.q.ListNotificationsBefore(ctx, sqlcdb.ListNotificationsBeforeParams{
			ID:    before,
			Limit: int64(limit),
		})
	} else {
		rows, err = db.q.ListNotifications(ctx, int64(limit))
	}
	if err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(rows))
	for _, r := range rows {
		n, err := notificationFromSqlcdb(r)
		if err != nil {
			return nil, fmt.Errorf("decode occurrences for %s: %w", r.ID, err)
		}
		out = append(out, *n)
	}
	return out, nil
}

func (db *SQLiteDatabase) GetNotificationByID(ctx context.Context, id string) (*Notification, error) {
	row, err := db.q.GetNotificationByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n, err := notificationFromSqlcdb(row)
	if err != nil {
		return nil, fmt.Errorf("decode occurrences for %s: %w", row.ID, err)
	}
	return n, nil
}

// PruneNotificationsByCount keeps the most recent `keep` rows and deletes the rest.
func (db *SQLiteDatabase) PruneNotificationsByCount(ctx context.Context, keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	return db.q.PruneNotificationsByCount(ctx, int64(keep))
}

// PruneNotificationsByAge deletes rows whose last_occurred_at is older than the cutoff.
func (db *SQLiteDatabase) PruneNotificationsByAge(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-olderThan)
	return db.q.PruneNotificationsByAge(ctx, cutoff)
}

// CountUnreadNotifications counts rows whose read_at is NULL.
func (db *SQLiteDatabase) CountUnreadNotifications(ctx context.Context) (int64, error) {
	return db.q.CountUnreadNotifications(ctx)
}

// MarkNotificationRead stamps read_at on a single row and returns the updated
// row. Re-marking an already-read row overwrites the timestamp.
func (db *SQLiteDatabase) MarkNotificationRead(ctx context.Context, id string, at time.Time) (*Notification, error) {
	return db.setNotificationReadAt(ctx, id, &at)
}

// MarkNotificationUnread clears read_at on a single row and returns it.
func (db *SQLiteDatabase) MarkNotificationUnread(ctx context.Context, id string) (*Notification, error) {
	return db.setNotificationReadAt(ctx, id, nil)
}

func (db *SQLiteDatabase) setNotificationReadAt(ctx context.Context, id string, at *time.Time) (*Notification, error) {
	if id == "" {
		return nil, errors.New("notification id is empty")
	}
	var affected int64
	var err error
	if at != nil {
		affected, err = db.q.MarkNotificationRead(ctx, sqlcdb.MarkNotificationReadParams{
			ReadAt: at,
			ID:     id,
		})
	} else {
		affected, err = db.q.MarkNotificationUnread(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, ErrNotFound
	}
	return db.GetNotificationByID(ctx, id)
}

// MarkAllNotificationsRead stamps read_at on every currently-unread row.
func (db *SQLiteDatabase) MarkAllNotificationsRead(ctx context.Context, at time.Time) error {
	return db.q.MarkAllNotificationsRead(ctx, &at)
}
