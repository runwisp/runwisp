-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- Normalize the run timestamp columns to the past-participle `*_at` convention
-- the rest of the schema already follows (created_at, deleted_at, read_at,
-- first_seen_at, last_occurred_at). `start_at`/`end_at` were the odd ones out.
-- No index references these columns, so a plain RENAME COLUMN suffices — no
-- table rebuild.
ALTER TABLE runs RENAME COLUMN start_at TO started_at;
ALTER TABLE runs RENAME COLUMN end_at TO ended_at;

-- pending_log_uploads.inserted_at stores unix seconds (INTEGER), unlike every
-- other `*_at` column, which is a DATETIME. The `_unix` suffix makes the
-- encoding explicit so a direct query doesn't mistake it for a datetime.
ALTER TABLE pending_log_uploads RENAME COLUMN inserted_at TO inserted_at_unix;
