-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- Baseline schema. This migration keeps IF NOT EXISTS so databases created
-- before the migration system existed adopt cleanly (their objects already
-- exist; this no-ops, then user_version is stamped to 1). Later migrations run
-- exactly once and must use plain DDL (no IF NOT EXISTS).

CREATE TABLE IF NOT EXISTS runs (
id                    TEXT PRIMARY KEY,
execution_id          TEXT,
task_name             TEXT NOT NULL DEFAULT '',
status                TEXT NOT NULL,
end_reason            TEXT,
exit_code             INTEGER NOT NULL DEFAULT 0,
start_at              DATETIME,
end_at                DATETIME,
triggered_by          TEXT NOT NULL,
created_at            DATETIME NOT NULL,
retry_attempt         INTEGER NOT NULL DEFAULT 0,
retry_of_run_id       TEXT,
instance_index        INTEGER NOT NULL DEFAULT 0,
params_json           TEXT,
deleted_at            DATETIME
);
CREATE INDEX IF NOT EXISTS idx_runs_execution_id ON runs(execution_id);
CREATE INDEX IF NOT EXISTS idx_runs_task_name ON runs(task_name);
CREATE INDEX IF NOT EXISTS idx_runs_deleted_at ON runs(deleted_at);
CREATE INDEX IF NOT EXISTS idx_runs_created_at_desc ON runs(created_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS task_registrations (
task_name     TEXT PRIMARY KEY,
first_seen_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS config_entries (
key   TEXT PRIMARY KEY,
value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
id                TEXT PRIMARY KEY,
fingerprint       TEXT NOT NULL,
kind              TEXT NOT NULL,
severity          TEXT NOT NULL,
task_name         TEXT NOT NULL DEFAULT '',
run_id            TEXT NOT NULL DEFAULT '',
title             TEXT NOT NULL DEFAULT '',
body              TEXT NOT NULL DEFAULT '',
count             INTEGER NOT NULL DEFAULT 1,
occurrences_json  TEXT NOT NULL DEFAULT '[]',
created_at        DATETIME NOT NULL,
last_occurred_at  DATETIME NOT NULL,
read_at           DATETIME
);
CREATE INDEX IF NOT EXISTS idx_notifications_fingerprint ON notifications(fingerprint);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_severity ON notifications(severity);
CREATE INDEX IF NOT EXISTS idx_notifications_read_at ON notifications(read_at);

CREATE TABLE IF NOT EXISTS pending_log_uploads (
execution_id         TEXT PRIMARY KEY,
upload_url            TEXT NOT NULL,
log_path              TEXT NOT NULL,
inserted_at           INTEGER NOT NULL
);
