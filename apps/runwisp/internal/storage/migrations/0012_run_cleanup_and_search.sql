-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

ALTER TABLE runs ADD COLUMN purge_pending INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_runs_purge_pending ON runs(deleted_at) WHERE purge_pending = 1;
CREATE INDEX idx_runs_task_search ON runs(task_name, created_at DESC, id DESC) WHERE deleted_at IS NULL;
