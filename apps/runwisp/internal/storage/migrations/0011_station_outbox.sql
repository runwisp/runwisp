-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- Execution updates waiting for the station's ack. AUTOINCREMENT keeps seq
-- increasing across deletes and restarts: sqlite_sequence remembers the
-- highest seq ever assigned, which the daemon reports as hello.lastSeq.
CREATE TABLE station_outbox (
seq          INTEGER PRIMARY KEY AUTOINCREMENT,
execution_id TEXT NOT NULL,
finished     INTEGER NOT NULL,
payload      TEXT NOT NULL
);

-- The station now hands out upload targets on request, so a pending upload
-- only records which execution still needs its log archived.
ALTER TABLE pending_log_uploads DROP COLUMN upload_url;
ALTER TABLE pending_log_uploads DROP COLUMN log_path;
