-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

-- name: PurgeExpiredSoftDeletes :many
DELETE FROM runs WHERE deleted_at IS NOT NULL AND deleted_at <= ?
RETURNING id, task_name, created_at;

-- name: SoftDeleteRunsByIDs :many
UPDATE runs SET deleted_at = sqlc.arg(deleted_at)
WHERE deleted_at IS NULL
  AND status = sqlc.arg(status_phase)
  AND id IN (sqlc.slice('ids'))
RETURNING id, task_name, created_at;

-- name: SoftDeleteRunsByFilter :many
UPDATE runs SET deleted_at = sqlc.arg(deleted_at)
WHERE deleted_at IS NULL
  AND status = sqlc.arg(status_phase)
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
  AND id NOT IN (sqlc.slice('except_ids'))
RETURNING id, task_name, created_at;

-- name: RestoreRunsByIDs :many
UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL
  AND id IN (sqlc.slice('ids'))
RETURNING id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, params_json, deleted_at;

-- name: RestoreRunsByFilter :many
UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL
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
  AND id NOT IN (sqlc.slice('except_ids'))
RETURNING id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, params_json, deleted_at;

-- name: ResolveSelectorIDsByIDs :many
-- Slice must come AFTER scalar args. sqlc emits `?N` for named scalars and
-- positional `?` for slices; it appends scalar params to queryParams first,
-- then expands the slice. Putting `id IN (?, ?...)` first would shift the
-- `?N` positions, so the slice is placed at the end.
SELECT id, task_name, created_at FROM runs
WHERE deleted_at IS NULL
  AND (sqlc.arg(bulk_status_filter) IS NULL OR status = sqlc.arg(bulk_status_filter))
  AND id IN (sqlc.slice('ids'));

-- name: ResolveSelectorIDsByFilter :many
SELECT id, task_name, created_at FROM runs
WHERE deleted_at IS NULL
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
  AND (sqlc.arg(bulk_status_filter) IS NULL OR status = sqlc.arg(bulk_status_filter))
  AND id NOT IN (sqlc.slice('except_ids'));
