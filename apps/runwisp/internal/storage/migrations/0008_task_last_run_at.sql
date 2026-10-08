-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- The newest created_at of any run a registered task ever had. Missed-tick
-- catch-up anchors on the task's last run; retention (keep_runs, keep_for,
-- storage.max_size) or an operator clearing history can delete that row, and
-- without this the anchor would fall back to first_seen_at and report every
-- tick since then as missed. Bumped by CreateRun, never by a delete.
ALTER TABLE task_registrations ADD COLUMN last_run_at DATETIME;

UPDATE task_registrations SET last_run_at = (
  SELECT MAX(created_at) FROM runs WHERE runs.task_name = task_registrations.task_name
);
