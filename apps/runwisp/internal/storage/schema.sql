-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: Apache-2.0

CREATE TABLE IF NOT EXISTS runs (
id                    TEXT PRIMARY KEY,
external_execution_id TEXT,
task_name             TEXT NOT NULL DEFAULT '',
status                VARCHAR(20) NOT NULL,
end_reason            VARCHAR(20),
exit_code             INTEGER NOT NULL DEFAULT 0,
start_at              DATETIME,
end_at                DATETIME,
triggered_by          VARCHAR(20) NOT NULL,
created_at            DATETIME NOT NULL,
retry_attempt         INTEGER NOT NULL DEFAULT 0,
retry_of_run_id       TEXT,
instance_index        INTEGER NOT NULL DEFAULT 0,
params_json           TEXT,
deleted_at            DATETIME
);
CREATE INDEX IF NOT EXISTS idx_runs_external_execution_id ON runs(external_execution_id);
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
external_execution_id TEXT PRIMARY KEY,
upload_url            TEXT NOT NULL,
log_path              TEXT NOT NULL,
inserted_at           INTEGER NOT NULL
);
