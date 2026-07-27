-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: ListNotifications :many
SELECT * FROM notifications ORDER BY id DESC LIMIT ?;

-- name: ListNotificationsBefore :many
SELECT * FROM notifications WHERE id < ? ORDER BY id DESC LIMIT ?;

-- name: GetNotificationByID :one
SELECT * FROM notifications WHERE id = ?;

-- name: CountUnreadNotifications :one
SELECT CAST(COUNT(*) AS INTEGER) FROM notifications WHERE read_at IS NULL;

-- name: PruneNotificationsByCount :execrows
DELETE FROM notifications WHERE id IN (
  SELECT id FROM notifications ORDER BY id DESC LIMIT -1 OFFSET ?
);

-- name: PruneNotificationsByAge :execrows
DELETE FROM notifications WHERE last_occurred_at < ?;

-- name: MarkNotificationRead :execrows
UPDATE notifications SET read_at = ? WHERE id = ?;

-- name: MarkNotificationUnread :execrows
UPDATE notifications SET read_at = NULL WHERE id = ?;

-- name: MarkAllNotificationsRead :exec
UPDATE notifications SET read_at = ? WHERE read_at IS NULL;

-- name: SelectExistingForFingerprint :one
SELECT id, count, occurrences_json FROM notifications
WHERE fingerprint = ? AND last_occurred_at >= ?
ORDER BY last_occurred_at DESC LIMIT 1;

-- name: InsertNotification :exec
INSERT INTO notifications (id, fingerprint, kind, severity, task_name, run_id,
  title, body, count, occurrences_json, created_at, last_occurred_at, read_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL);

-- name: UpdateNotificationCoalesced :exec
UPDATE notifications
SET count = ?, occurrences_json = ?, last_occurred_at = ?, title = ?, body = ?, read_at = NULL
WHERE id = ?;
