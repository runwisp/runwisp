-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- execution_id identifies a cloud-dispatched run and is used as a lookup key
-- everywhere (execution-stop, log replay, orphan-upload recovery). Uniqueness
-- was previously enforced only by a check-then-act guard in application code,
-- with the DB carrying a plain (non-unique) index — so a race that let two
-- dispatches for the same execution_id both insert would leave two rows sharing
-- one key and every LIMIT 1 lookup would non-deterministically pick one.
--
-- Replace the plain index with a partial UNIQUE index scoped to live rows,
-- matching how execution_id is always queried (AND deleted_at IS NULL). Local
-- (non-cloud) runs leave execution_id NULL and are excluded by the predicate.

-- Resolve any pre-existing duplicates first so the UNIQUE index can be built:
-- keep the earliest row per execution_id (ULIDs sort by creation time) and
-- soft-delete the rest. Reusing each row's own created_at as the deleted_at
-- marker guarantees the value is in the exact format the app writes, and marks
-- the bogus duplicates immediately purge-eligible.
UPDATE runs SET deleted_at = created_at
WHERE execution_id IS NOT NULL
  AND deleted_at IS NULL
  AND id NOT IN (
    SELECT MIN(id) FROM runs
    WHERE execution_id IS NOT NULL AND deleted_at IS NULL
    GROUP BY execution_id
  );

DROP INDEX IF EXISTS idx_runs_execution_id;
CREATE UNIQUE INDEX idx_runs_execution_id ON runs(execution_id)
  WHERE execution_id IS NOT NULL AND deleted_at IS NULL;
