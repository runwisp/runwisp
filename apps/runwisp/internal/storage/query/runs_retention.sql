-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

-- name: SelectOldRunsByAge :many
-- Only terminal (ended) runs are eligible for retention: a run that is still
-- pending or running must never have its row or live log files removed.
SELECT * FROM runs
WHERE task_name = ? AND created_at < ? AND status = 'ended' AND deleted_at IS NULL
LIMIT ?;

-- name: SelectOldRunsByCount :many
-- Count-based retention likewise ranges over ended runs only, so in-flight
-- runs neither count against the cap nor get purged.
SELECT * FROM runs
WHERE task_name = ? AND status = 'ended' AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT ? OFFSET ?;
