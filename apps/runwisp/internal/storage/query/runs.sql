-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: CreateRun :exec
INSERT INTO runs (id, execution_id, task_name, status, end_reason,
  exit_code, started_at, ended_at, triggered_by, created_at, retry_attempt,
  retry_of_run_id, instance_index, params_json, is_failure)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateRun :execrows
UPDATE runs SET execution_id = ?, task_name = ?, status = ?,
  end_reason = ?, exit_code = ?, started_at = ?, ended_at = ?, triggered_by = ?,
  created_at = ?, retry_attempt = ?, retry_of_run_id = ?, instance_index = ?,
  params_json = ?, is_failure = ?
WHERE id = ?;

-- name: GetRun :one
SELECT * FROM runs WHERE id = ? AND deleted_at IS NULL LIMIT 1;

-- name: GetRunByExecutionID :one
SELECT * FROM runs WHERE execution_id = ? AND deleted_at IS NULL LIMIT 1;

-- name: CountRunsFiltered :one
SELECT COUNT(*) FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)));

-- name: QueryRunsCreatedAtDesc :many
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStartAtAsc :many
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY COALESCE(started_at, created_at) ASC, created_at ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsTaskNameDesc :many
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
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
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY (COALESCE(julianday(ended_at) - julianday(started_at), 0)) DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsDurationAsc :many
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index, params_json, is_failure
FROM runs WHERE deleted_at IS NULL
  AND ((sqlc.arg(status_set) IS NULL AND sqlc.arg(match_failure) = 0)
       OR instr(sqlc.arg(status_set), '|' || status || '|') > 0
       OR (end_reason IS NOT NULL AND instr(sqlc.arg(status_set), '|' || end_reason || '|') > 0)
       OR (sqlc.arg(match_failure) = 1 AND is_failure = 1))
  AND (sqlc.arg(created_after) IS NULL OR created_at >= sqlc.arg(created_after))
  AND (sqlc.arg(created_before) IS NULL OR created_at <= sqlc.arg(created_before))
  AND (sqlc.arg(triggered_by_filter) IS NULL OR triggered_by = sqlc.arg(triggered_by_filter))
  AND (sqlc.arg(exit_code_min) IS NULL OR exit_code >= sqlc.arg(exit_code_min))
  AND (sqlc.arg(exit_code_max) IS NULL OR exit_code <= sqlc.arg(exit_code_max))
  AND (sqlc.arg(retries_only) IS NULL OR retry_attempt > 0)
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
ORDER BY (COALESCE(julianday(ended_at) - julianday(started_at), 0)) ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: GetRunSummary :one
-- 'missed' is counted on its own and deliberately excluded from 'failed':
-- a missed run never executed, so folding it into the execution-failure
-- count (and last_failure timestamp) would skew failure metrics.
-- 'failed' and last_failure read the persisted is_failure bit (each run's
-- `failures` policy, resolved at termination) rather than a hardcoded end_reason
-- set, so this metric can never drift from the rest of the failure readouts.
SELECT
  CAST(COUNT(*) AS INTEGER) AS total,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'succeeded' THEN 1 ELSE 0 END), 0) AS INTEGER) AS success,
  CAST(COALESCE(SUM(CASE WHEN is_failure = 1 AND end_reason != 'missed'
                         THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'missed' THEN 1 ELSE 0 END), 0) AS INTEGER) AS missed,
  (SELECT ended_at FROM runs
   WHERE is_failure = 1 AND end_reason != 'missed'
     AND deleted_at IS NULL
   ORDER BY ended_at DESC LIMIT 1) AS ended_at
FROM runs WHERE deleted_at IS NULL;

-- name: MarkCrashedRuns :execrows
-- Boot-time crash recovery marks these orphans is_failure=1 using the default
-- classification: a task that demoted 'crashed' from its `failures` would
-- mis-tag its own boot-marked orphans, but that combination is exotic and the
-- alternative (loading every task's policy in the recovery path) is not worth it.
UPDATE runs SET status = 'ended', end_reason = 'crashed', ended_at = ?, exit_code = -2, is_failure = 1
WHERE status = 'running' AND ended_at IS NULL AND deleted_at IS NULL;

-- name: GetPendingRuns :many
-- Full table projection in column order so sqlc reuses the Run model struct
-- (is_failure is last because the migration appended the column).
SELECT id, execution_id, task_name, status, end_reason, exit_code,
  started_at, ended_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, params_json, deleted_at, is_failure
FROM runs WHERE status = 'pending' AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: GetLastRunByTask :one
SELECT * FROM runs WHERE task_name = ? AND deleted_at IS NULL
ORDER BY created_at DESC LIMIT 1;

-- name: DeleteRun :exec
DELETE FROM runs WHERE id = ?;

-- name: DeleteRunsByIDs :exec
DELETE FROM runs WHERE id IN (sqlc.slice('ids'));
