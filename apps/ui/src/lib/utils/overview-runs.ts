// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run } from "@runwisp/common";
import { runPhaseOrder } from "@runwisp/ui";
import { sortByCreatedAtDesc } from "$lib/utils/sort";

// upsertPhaseGuarded folds one snapshot run into the list without ever
// regressing a run's phase. A snapshot fetched before an SSE `run.completed`
// lands carries the run as still "running"; merging it through this guard keeps
// the SSE-advanced "ended" row intact instead of reverting it.
function upsertPhaseGuarded(list: Run[], run: Run): Run[] {
    const idx = list.findIndex((r) => r.id === run.id);
    if (idx === -1) return [...list, run];
    const existing = list[idx];
    if (!existing) return list;
    if (runPhaseOrder(run.status) < runPhaseOrder(existing.status)) return list;
    const copy = [...list];
    copy[idx] = run;
    return copy;
}

// mergeRecentRuns reconciles a freshly-fetched snapshot of recent runs into the
// live (SSE-fed) list, preserving the newest known phase for each run, and
// returns the newest `limit` runs.
export function mergeRecentRuns(existing: Run[], snapshot: Run[], limit: number): Run[] {
    let merged = existing;
    for (const run of snapshot) merged = upsertPhaseGuarded(merged, run);
    return sortByCreatedAtDesc(merged).slice(0, limit);
}

// mergeRunningRuns reconciles a snapshot of running runs into the live list.
// The phase guard drops a stale snapshot's "running" copy of a run the live
// state already advanced past, and the final filter keeps only rows that are
// still running.
export function mergeRunningRuns(existing: Run[], snapshot: Run[], limit: number): Run[] {
    let merged = existing;
    for (const run of snapshot) merged = upsertPhaseGuarded(merged, run);
    return sortByCreatedAtDesc(merged)
        .filter((run) => run.status === "running")
        .slice(0, limit);
}
