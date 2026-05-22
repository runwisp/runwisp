-- name: CreateRun :exec
INSERT INTO runs (id, external_execution_id, task_name, status, end_reason,
  exit_code, start_at, end_at, triggered_by, created_at, retry_attempt,
  retry_of_run_id, instance_index)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateRun :exec
UPDATE runs SET external_execution_id = ?, task_name = ?, status = ?,
  end_reason = ?, exit_code = ?, start_at = ?, end_at = ?, triggered_by = ?,
  created_at = ?, retry_attempt = ?, retry_of_run_id = ?, instance_index = ?
WHERE id = ?;

-- name: GetRun :one
SELECT * FROM runs WHERE id = ? AND deleted_at IS NULL LIMIT 1;

-- name: GetRunByExternalExecutionID :one
SELECT * FROM runs WHERE external_execution_id = ? AND deleted_at IS NULL LIMIT 1;

-- name: CountRuns :one
SELECT COUNT(*) FROM runs WHERE task_name = ? AND deleted_at IS NULL;

-- name: CountRunsFiltered :one
SELECT COUNT(*) FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(end_reason_gate) = '' OR end_reason = sqlc.arg(end_reason_value))
  AND (sqlc.arg(status_gate) = '' OR status = sqlc.arg(status_value))
  AND (sqlc.arg(task_name_gate) = '' OR task_name = sqlc.arg(task_name_value))
  AND (sqlc.arg(search_gate) = ''
       OR (task_name LIKE sqlc.arg(search_pattern_name)
           OR id LIKE sqlc.arg(search_pattern_id)));

-- name: GetRunSummary :one
SELECT
  CAST(COUNT(*) AS INTEGER) AS total,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'success' THEN 1 ELSE 0 END), 0) AS INTEGER) AS success,
  CAST(COALESCE(SUM(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow')
                         THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed,
  MAX(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow')
           THEN end_at END) AS last_failure
FROM runs WHERE deleted_at IS NULL;

-- name: MarkCrashedRuns :execrows
UPDATE runs SET status = 'ended', end_reason = 'crashed', end_at = ?, exit_code = -2
WHERE status = 'running' AND end_at IS NULL AND deleted_at IS NULL;

-- name: GetPendingRuns :many
SELECT * FROM runs WHERE status = 'pending' AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: GetLastRunByTask :one
SELECT * FROM runs WHERE task_name = ? AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteRun :exec
DELETE FROM runs WHERE id = ?;
