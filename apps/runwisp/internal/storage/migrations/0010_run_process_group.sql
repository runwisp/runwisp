-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- The process group a running run's process leads, and that leader's
-- boot-qualified start time (bootid.Process). A daemon booting after a crash
-- kills a group still led by the same process before marking its run crashed,
-- so the orphan can't run on next to the next firing. 0/'' when the backend
-- has no process group, the start time couldn't be read, and for older rows.
ALTER TABLE runs ADD COLUMN pgid INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN process_start TEXT NOT NULL DEFAULT '';
