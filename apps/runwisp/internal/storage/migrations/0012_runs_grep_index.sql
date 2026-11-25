-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- log:grep pages through a daemon's station executions newest start first
-- (ListGrepRuns, CountGrepRuns). task_name rides along so a task filter is
-- checked on the index entry without reading the row.
CREATE INDEX idx_runs_grep ON runs(COALESCE(started_at, created_at) DESC, id DESC, task_name)
  WHERE execution_id IS NOT NULL AND deleted_at IS NULL;
