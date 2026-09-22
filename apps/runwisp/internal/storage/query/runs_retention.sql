-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: SelectOldRunsByAge :many
-- Only terminal (ended) runs are eligible for retention: a run that is still
-- pending or running must never have its row or live log files removed.
-- ORDER BY created_at ASC so a backlog larger than the retention batch size
-- evicts the oldest runs first, rather than an arbitrary LIMIT slice.
SELECT * FROM runs
WHERE task_name = ? AND created_at < ? AND status = 'ended' AND deleted_at IS NULL
ORDER BY created_at ASC
LIMIT ?;

-- name: SelectOldRunsByCount :many
-- Count-based retention likewise ranges over ended runs only, so in-flight
-- runs neither count against the cap nor get purged.
SELECT * FROM runs
WHERE task_name = ? AND status = 'ended' AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT ? OFFSET ?;
