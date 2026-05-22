-- name: PurgeExpiredSoftDeletes :many
DELETE FROM runs WHERE deleted_at IS NOT NULL AND deleted_at <= ?
RETURNING id, task_name, created_at;
