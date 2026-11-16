-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- name: InsertOutboxUpdate :one
INSERT INTO station_outbox (execution_id, finished, payload)
VALUES (?, ?, ?)
RETURNING seq;

-- name: ListOutboxUpdates :many
SELECT seq, execution_id, finished, payload FROM station_outbox
WHERE seq > ?
ORDER BY seq
LIMIT ?;

-- name: DeleteOutboxUpdatesThrough :exec
DELETE FROM station_outbox WHERE seq <= ?;

-- name: ListOutboxExecutions :many
SELECT execution_id, CAST(MAX(finished) AS INTEGER) AS finished FROM station_outbox
GROUP BY execution_id
ORDER BY execution_id;

