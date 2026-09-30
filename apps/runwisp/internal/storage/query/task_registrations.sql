-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: EnsureTaskRegistered :exec
INSERT OR IGNORE INTO task_registrations (task_name, first_seen_at)
VALUES (?, ?);

-- name: GetTaskRegistration :one
SELECT * FROM task_registrations WHERE task_name = ?;

-- name: PauseTaskSchedule :exec
-- Upsert: the registration row normally exists, but a failed registration
-- write must not make a pause silently disappear. An already-paused task keeps
-- its original paused_at.
INSERT INTO task_registrations (task_name, first_seen_at, paused_at)
VALUES (sqlc.arg(task_name), sqlc.arg(paused_at), sqlc.arg(paused_at))
ON CONFLICT(task_name) DO UPDATE SET paused_at = COALESCE(task_registrations.paused_at, excluded.paused_at);

-- name: ResumeTaskSchedule :exec
UPDATE task_registrations SET paused_at = NULL, resumed_at = sqlc.arg(resumed_at)
WHERE task_name = sqlc.arg(task_name) AND paused_at IS NOT NULL;

-- name: ListPausedTaskSchedules :many
SELECT task_name, paused_at FROM task_registrations WHERE paused_at IS NOT NULL;
