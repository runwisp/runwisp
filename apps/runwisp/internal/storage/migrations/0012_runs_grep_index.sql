-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- log:grep pages through a daemon's station executions newest start first
-- (ListGrepRuns, CountGrepRuns). task_name and execution_id ride along so the
-- task filter and whether a run is a station execution are checked on the
-- index entry without reading the row.
CREATE INDEX idx_runs_grep ON runs(COALESCE(started_at, created_at) DESC, id DESC, task_name, execution_id)
  WHERE deleted_at IS NULL;
