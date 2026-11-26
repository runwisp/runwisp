-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: CreateRun :exec
INSERT INTO runs (id, execution_id, task_name, status, end_reason,
  exit_code, started_at, ended_at, triggered_by, created_at, retry_attempt,
  retry_of_run_id, instance_index, params_json, is_failure, peak_memory_bytes,
  cpu_time_ms, stdout_lines, stderr_lines)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateRun :execrows
UPDATE runs SET execution_id = ?, task_name = ?, status = ?,
  end_reason = ?, exit_code = ?, started_at = ?, ended_at = ?, triggered_by = ?,
  created_at = ?, retry_attempt = ?, retry_of_run_id = ?, instance_index = ?,
  params_json = ?, is_failure = ?, peak_memory_bytes = ?, cpu_time_ms = ?,
  stdout_lines = ?, stderr_lines = ?, pgid = ?, process_start = ?
WHERE id = ?;

-- name: GetRun :one
SELECT * FROM runs WHERE id = ? AND deleted_at IS NULL LIMIT 1;

-- name: GetLastRun :one
-- The newest run of a task that started or failed: a missed run counts when
-- it is classed as a failure, but pending and skipped rows never mask the
-- state of the run before them.
SELECT * FROM runs
WHERE task_name = ? AND deleted_at IS NULL
  AND (started_at IS NOT NULL OR is_failure = 1)
ORDER BY COALESCE(started_at, created_at) DESC, id DESC
LIMIT 1;

-- name: PreviousRunIsFailure :one
-- The failure bit of the task's newest ended run other than the given one,
-- counted the way GetLastRun counts: a missed run only when it is a failure.
SELECT is_failure FROM runs
WHERE task_name = ? AND id != ? AND deleted_at IS NULL AND status = 'ended'
  AND (started_at IS NOT NULL OR is_failure = 1)
ORDER BY COALESCE(started_at, created_at) DESC, id DESC
LIMIT 1;

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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0));

-- name: QueryRunsCreatedAtDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY created_at DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsCreatedAtAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY created_at ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStartAtDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY COALESCE(started_at, created_at) DESC, created_at DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStartAtAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY COALESCE(started_at, created_at) ASC, created_at ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsTaskNameDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY task_name DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsTaskNameAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY task_name ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStatusDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY status DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsStatusAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY status ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsExitCodeDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY exit_code DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsExitCodeAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY exit_code ASC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsDurationDesc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY (COALESCE(julianday(ended_at) - julianday(started_at), 0)) DESC, id DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

-- name: QueryRunsDurationAsc :many
SELECT * FROM runs WHERE deleted_at IS NULL
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
  AND (sqlc.arg(search_filter) IS NULL OR (instr(lower(task_name), sqlc.arg(search_term)) > 0 OR instr(lower(id), sqlc.arg(search_term)) > 0))
ORDER BY (COALESCE(julianday(ended_at) - julianday(started_at), 0)) ASC, id DESC LIMIT sqlc.arg(rows_limit) OFFSET sqlc.arg(rows_offset);

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

-- name: GetRunningRuns :many
SELECT * FROM runs WHERE status = 'running' AND ended_at IS NULL AND deleted_at IS NULL;

-- name: MarkCrashedRuns :execrows
-- Boot-time crash recovery marks these orphans is_failure=1 using the default
-- classification: a task that demoted 'crashed' from its `failures` would
-- mis-tag its own boot-marked orphans, but that combination is exotic and the
-- alternative (loading every task's policy in the recovery path) is not worth it.
UPDATE runs SET status = 'ended', end_reason = 'crashed', ended_at = ?, exit_code = -2, is_failure = 1
WHERE status = 'running' AND ended_at IS NULL AND deleted_at IS NULL;

-- name: GetPendingRuns :many
SELECT * FROM runs WHERE status = 'pending' AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: DeleteRun :exec
DELETE FROM runs WHERE id = ?;

-- name: DeleteRunsByIDs :exec
DELETE FROM runs WHERE id IN (sqlc.slice('ids'));

-- name: ListGrepRuns :many
-- One keyset page of station executions for log:grep, newest start first:
-- the runs sorting below the cursor (before_start, before_id) that started at
-- or after since. A station execution is a run the station hears of: a
-- dispatched one (an execution id), or a service instance's run, which the
-- daemon starts itself: one of service_tasks, or one whose log archive is
-- still pending (its service since removed). Times are bound as text in the
-- stored format; sort_key is the row's raw start text, the next page's
-- cursor. INDEXED BY pins idx_runs_grep: without statistics the planner
-- would take idx_runs_deleted_at and sort every row instead. SQLite bounds an
-- index scan by a plain comparison on the start but not by the row value, so
-- the row value only breaks ties under the redundant start <= before_start;
-- the || '' makes sqlc bind text where CAST would stop the index from
-- bounding the scan. The slices come last and the page size is fixed because
-- sqlc numbers a parameter after a slice wrongly.
SELECT id, execution_id, task_name, created_at, started_at,
  CAST(COALESCE(started_at, created_at) AS TEXT) AS sort_key
FROM runs INDEXED BY idx_runs_grep
WHERE deleted_at IS NULL
  AND COALESCE(started_at, created_at) <= sqlc.arg(before_start) || ''
  AND (COALESCE(started_at, created_at), id) < (sqlc.arg(before_start) || '', sqlc.arg(before_id) || '')
  AND COALESCE(started_at, created_at) >= sqlc.arg(since) || ''
  AND (sqlc.arg(all_tasks) OR task_name IN (sqlc.slice(task_names)))
  AND (execution_id IS NOT NULL OR task_name IN (sqlc.slice(service_tasks))
    OR id IN (SELECT execution_id FROM pending_log_uploads))
ORDER BY COALESCE(started_at, created_at) DESC, id DESC
LIMIT 500;

-- name: CountGrepRuns :one
-- How many runs ListGrepRuns pages through from the start, read from
-- idx_runs_grep alone.
SELECT COUNT(*) FROM runs INDEXED BY idx_runs_grep
WHERE deleted_at IS NULL
  AND COALESCE(started_at, created_at) < sqlc.arg(before_start) || ''
  AND COALESCE(started_at, created_at) >= sqlc.arg(since) || ''
  AND (sqlc.arg(all_tasks) OR task_name IN (sqlc.slice(task_names)))
  AND (execution_id IS NOT NULL OR task_name IN (sqlc.slice(service_tasks))
    OR id IN (SELECT execution_id FROM pending_log_uploads));
