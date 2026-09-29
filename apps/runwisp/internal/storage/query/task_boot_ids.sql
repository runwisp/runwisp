-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: GetTaskBootID :one
SELECT boot_id FROM task_boot_ids WHERE task_name = ?;

-- name: SetTaskBootID :exec
INSERT INTO task_boot_ids (task_name, boot_id) VALUES (?, ?)
ON CONFLICT(task_name) DO UPDATE SET boot_id = excluded.boot_id;
