-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

-- name: CreateRun :exec
INSERT INTO runs (id, external_execution_id, task_name, status, end_reason,
  exit_code, start_at, end_at, triggered_by, created_at, retry_attempt,
  retry_of_run_id, instance_index, params_json)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateRun :exec
UPDATE runs SET external_execution_id = ?, task_name = ?, status = ?,
  end_reason = ?, exit_code = ?, start_at = ?, end_at = ?, triggered_by = ?,
  created_at = ?, retry_attempt = ?, retry_of_run_id = ?, instance_index = ?,
  params_json = ?
WHERE id = ?;

-- name: GetRun :one
SELECT * FROM runs WHERE id = ? AND deleted_at IS NULL LIMIT 1;

-- name: GetRunByExternalExecutionID :one
SELECT * FROM runs WHERE external_execution_id = ? AND deleted_at IS NULL LIMIT 1;

-- name: CountRuns :one
SELECT COUNT(*) FROM runs WHERE task_name = ? AND deleted_at IS NULL;

-- name: CountRunsFiltered :one
SELECT COUNT(*) FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)));

-- name: QueryRunsCreatedAtDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY created_at DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsCreatedAtAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY created_at ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStartAtDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY COALESCE(start_at, created_at) DESC, created_at DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStartAtAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY COALESCE(start_at, created_at) ASC, created_at ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsTaskNameDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY task_name DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsTaskNameAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY task_name ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStatusDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY status DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStatusAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY status ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsExitCodeDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY exit_code DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsExitCodeAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY exit_code ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsDurationDesc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY (COALESCE(julianday(end_at) - julianday(start_at), 0)) DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsDurationAsc :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(status_set) IS NULL
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY (COALESCE(julianday(end_at) - julianday(start_at), 0)) ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: GetRunSummary :one
-- 'missed' is counted on its own and deliberately excluded from 'failed':
-- a missed run never executed, so folding it into the execution-failure
-- count (and last_failure timestamp) would skew failure metrics.
-- The failure set below must mirror runtime/retry.IsFailureReason (Go): keep
-- them in sync when a new failure end_reason is added, or this summary count and
-- the failed run metric (runwisp_runs_total status=failed) will undercount.
SELECT
  CAST(COUNT(*) AS INTEGER) AS total,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'success' THEN 1 ELSE 0 END), 0) AS INTEGER) AS success,
  CAST(COALESCE(SUM(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow','start_failed')
                         THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'missed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS missed,
  (SELECT end_at FROM runs
   WHERE end_reason IN ('failed','crashed','timeout','log_overflow','start_failed')
     AND deleted_at IS NULL
   ORDER BY end_at DESC LIMIT 1) AS end_at
FROM runs WHERE deleted_at IS NULL;

-- name: MarkCrashedRuns :execrows
UPDATE runs SET status = 'ended', end_reason = 'crashed', end_at = ?, exit_code = -2
WHERE status = 'running' AND end_at IS NULL AND deleted_at IS NULL;

-- name: GetPendingRuns :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, params_json, deleted_at
FROM runs WHERE status = 'pending' AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: GetLastRunByTask :one
SELECT * FROM runs WHERE task_name = ? AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteRun :exec
DELETE FROM runs WHERE id = ?;

-- name: DeleteRunsByIDs :exec
DELETE FROM runs WHERE id IN (sqlc.slice('ids'));
