-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- The boot a run_on_start = "boot" task last fired in, so a daemon restart
-- within the same machine or container boot does not fire it again. boot_id is
-- the opaque identity from internal/bootid.
CREATE TABLE task_boot_ids (
task_name TEXT PRIMARY KEY,
boot_id   TEXT NOT NULL
);
