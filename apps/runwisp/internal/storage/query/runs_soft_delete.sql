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
  AND (sqlc.arg(end_reason_filter) IS NULL OR end_reason = sqlc.arg(end_reason_filter))
  AND (sqlc.arg(status_phase_filter) IS NULL OR status = sqlc.arg(status_phase_filter))
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
  AND id NOT IN (sqlc.slice('except_ids'))
RETURNING id, task_name, created_at;

-- name: RestoreRunsByIDs :exec
UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL
  AND id IN (sqlc.slice('ids'));

-- name: RestoreRunsByFilter :exec
UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL
  AND (sqlc.arg(end_reason_filter) IS NULL OR end_reason = sqlc.arg(end_reason_filter))
  AND (sqlc.arg(status_phase_filter) IS NULL OR status = sqlc.arg(status_phase_filter))
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
  AND id NOT IN (sqlc.slice('except_ids'));

-- name: SelectRestoredRunsByIDs :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, deleted_at
FROM runs WHERE deleted_at IS NULL
  AND id IN (sqlc.slice('ids'));

-- name: SelectRestoredRunsByFilter :many
SELECT id, external_execution_id, task_name, status, end_reason, exit_code,
  start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id,
  instance_index, deleted_at
FROM runs WHERE deleted_at IS NULL
  AND (sqlc.arg(end_reason_filter) IS NULL OR end_reason = sqlc.arg(end_reason_filter))
  AND (sqlc.arg(status_phase_filter) IS NULL OR status = sqlc.arg(status_phase_filter))
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
  AND id NOT IN (sqlc.slice('except_ids'));

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
  AND (sqlc.arg(end_reason_filter) IS NULL OR end_reason = sqlc.arg(end_reason_filter))
  AND (sqlc.arg(status_phase_filter) IS NULL OR status = sqlc.arg(status_phase_filter))
  AND (sqlc.arg(task_name_filter) IS NULL OR task_name = sqlc.arg(task_name_filter))
  AND (sqlc.arg(search_filter) IS NULL OR (task_name LIKE sqlc.arg(search_pattern) OR id LIKE sqlc.arg(search_pattern)))
  AND (sqlc.arg(bulk_status_filter) IS NULL OR status = sqlc.arg(bulk_status_filter))
  AND id NOT IN (sqlc.slice('except_ids'));
