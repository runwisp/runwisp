-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

-- name: SelectOldRunsByAge :many
SELECT * FROM runs
WHERE task_name = ? AND created_at < ? AND deleted_at IS NULL
LIMIT ?;

-- name: SelectOldRunsByCount :many
SELECT * FROM runs
WHERE task_name = ? AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT ? OFFSET ?;
