-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- is_failure is the run's failure classification, resolved once at termination
-- from the task's `failures` policy and persisted so every reader (the failed
-- metric, UI attention badges, notification routing) shares one definition
-- instead of re-deriving from end_reason. New rows always write it explicitly.
ALTER TABLE runs ADD COLUMN is_failure INTEGER NOT NULL DEFAULT 0;

-- Backfill history with the built-in default classification (the set that was
-- live before per-task `failures` existed). This IN-list is a frozen
-- point-in-time snapshot, not a live source of truth: newer runs are classified
-- by the daemon and stored directly.
UPDATE runs SET is_failure = 1
WHERE end_reason IN ('failed','timeout','crashed','log_overflow','start_failed','missed');
