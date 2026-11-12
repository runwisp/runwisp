-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: UpsertPendingLogUpload :exec
INSERT INTO pending_log_uploads (execution_id, inserted_at_unix)
VALUES (?, ?)
ON CONFLICT(execution_id) DO UPDATE SET
  inserted_at_unix = excluded.inserted_at_unix;

-- name: DeletePendingLogUpload :exec
DELETE FROM pending_log_uploads WHERE execution_id = ?;

-- name: ListPendingLogUploads :many
SELECT execution_id, inserted_at_unix FROM pending_log_uploads;
