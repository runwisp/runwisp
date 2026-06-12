-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

-- name: UpsertPendingLogUpload :exec
INSERT INTO pending_log_uploads (external_execution_id, upload_url, log_path, inserted_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(external_execution_id) DO UPDATE SET
  upload_url = excluded.upload_url,
  log_path = excluded.log_path,
  inserted_at = excluded.inserted_at;

-- name: DeletePendingLogUpload :exec
DELETE FROM pending_log_uploads WHERE external_execution_id = ?;

-- name: ListPendingLogUploads :many
SELECT external_execution_id, upload_url, log_path, inserted_at FROM pending_log_uploads;
