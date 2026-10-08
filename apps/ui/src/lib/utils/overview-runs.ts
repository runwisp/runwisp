// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Run } from "@runwisp/common";
import { runPhaseOrder } from "@runwisp/ui";

// upsertRun folds one run into the list without ever regressing its phase. A
// snapshot (or stale HTTP response) fetched before an SSE `run.completed` lands
// carries the run as still "running"; the guard keeps the SSE-advanced "ended"
// row intact instead of reverting it.
function upsertRun(list: Run[], run: Run): Run[] {
    const idx = list.findIndex((r) => r.id === run.id);
    if (idx === -1) return [...list, run];
    const existing = list[idx];
    if (!existing) return list;
    if (runPhaseOrder(run.status) < runPhaseOrder(existing.status)) return list;
    const copy = [...list];
    copy[idx] = run;
    return copy;
}

/**
 * Reconcile `incoming` runs (an SSE event or a fetched snapshot) into the live
 * list, preserving the newest known phase of each run, and return the newest
 * `limit` runs that pass `keep`.
 */
export function mergeRuns(
    existing: Run[],
    incoming: Run[],
    limit: number,
    keep: (run: Run) => boolean = () => true,
): Run[] {
    let merged = existing;
    for (const run of incoming) merged = upsertRun(merged, run);
    return [...merged]
        .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
        .filter(keep)
        .slice(0, limit);
}
