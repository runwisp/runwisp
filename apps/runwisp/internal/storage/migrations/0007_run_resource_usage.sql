-- SPDX-FileCopyrightText: PoppyCake, s.r.o.
-- SPDX-License-Identifier: GPL-3.0-or-later

-- A run's resource totals, measured for shell runs only: the highest resident
-- memory seen in its process group and its user+system CPU time. NULL when not
-- measured (other backends, runs too short to sample, older rows).
ALTER TABLE runs ADD COLUMN peak_memory_bytes INTEGER;
ALTER TABLE runs ADD COLUMN cpu_time_ms INTEGER;
