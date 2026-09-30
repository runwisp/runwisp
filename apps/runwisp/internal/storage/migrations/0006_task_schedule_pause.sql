-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- An operator can pause a task's cron schedule at runtime (POST
-- /api/tasks/{name}/pause, `runwisp pause`). paused_at is set while the pause
-- is in force; resumed_at is the last time one was lifted, so missed-tick
-- catch-up anchors no earlier than it and never counts a paused window as
-- downtime.
ALTER TABLE task_registrations ADD COLUMN paused_at DATETIME;
ALTER TABLE task_registrations ADD COLUMN resumed_at DATETIME;
