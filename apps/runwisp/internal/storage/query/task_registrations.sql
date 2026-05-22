-- name: EnsureTaskRegistered :exec
INSERT OR IGNORE INTO task_registrations (task_name, first_seen_at)
VALUES (?, ?);

-- name: GetTaskRegistration :one
SELECT * FROM task_registrations WHERE task_name = ?;
