-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- How many lines a run's process printed on stdout and on stderr, counting
-- lines the log dropped. NULL for runs that never executed and older rows.
ALTER TABLE runs ADD COLUMN stdout_lines INTEGER;
ALTER TABLE runs ADD COLUMN stderr_lines INTEGER;
