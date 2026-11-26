-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- log:grep also scans the runs that started before its range but overlap it:
-- still running (no ended_at, sorting as '9', after every stored time) or
-- ended inside or after it (ListGrepRunsStartedBefore). The start, task_name
-- and execution_id ride along so the range and filters are checked on the
-- index entry.
CREATE INDEX idx_runs_grep_end ON runs(COALESCE(ended_at, '9'), COALESCE(started_at, created_at), id, task_name, execution_id)
  WHERE deleted_at IS NULL;
