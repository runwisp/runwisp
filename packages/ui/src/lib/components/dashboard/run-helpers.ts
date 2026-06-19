// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run } from "@runwisp/common";
import { formatDuration } from "../../utils/format.js";

export function runDuration(run: Pick<Run, "start_at" | "end_at">): string | undefined {
    if (!run.start_at) return undefined;
    const start = new Date(run.start_at).getTime();
    const end = run.end_at ? new Date(run.end_at).getTime() : Date.now();
    return formatDuration(end - start);
}

/**
 * Gap between when a run was scheduled (`created_at`, the cron tick) and when
 * it actually started (`start_at`), formatted — or undefined when the two are
 * within a second of each other. This is the visible face of `jitter`: a
 * jittered run is created at its tick but starts later inside the window, and
 * this is by how much. It also surfaces queue-wait, since a queued run is
 * created when it joins the line and started when the line clears.
 */
export function runStartDelay(run: Pick<Run, "created_at" | "start_at">): string | undefined {
    if (!run.start_at) return undefined;
    const delay = new Date(run.start_at).getTime() - new Date(run.created_at).getTime();
    if (delay < 1000) return undefined;
    return formatDuration(delay);
}

/**
 * Display suffix for a run's instance slot. A service configured with more than
 * one instance gets a 1-based suffix (`#1`, `#2`, …) on every one of its runs;
 * a single-instance task (or any non-service, where `instanceCount` is 1)
 * returns an empty string so the bare task name is shown. `instanceIndex` is
 * the stored 0-based slot; `instanceCount` is the task's currently configured
 * instance count.
 */
export function instanceSuffix(instanceIndex: number, instanceCount: number): string {
    if (instanceCount > 1) {
        return `#${String(instanceIndex + 1)}`;
    }
    return "";
}

/** Human label for why a run fired (the `triggered_by` source). */
export function formatTriggeredByLabel(triggeredBy: Run["triggered_by"]): string {
    if (triggeredBy === "api") return "API";
    if (triggeredBy === "cron") return "Cron";
    if (triggeredBy === "service") return "Service";
    if (triggeredBy === "startup") return "Startup";
    return "Cloud";
}

/**
 * "retry #N" label when a run is a retry of an earlier one, else undefined.
 * A run is a retry if it carries a positive attempt number or points back at
 * the run it re-attempts.
 */
export function runRetryLabel(
    run: Pick<Run, "retry_attempt" | "retry_of_run_id">,
): string | undefined {
    if (run.retry_attempt > 0 || run.retry_of_run_id) {
        return `retry #${String(run.retry_attempt)}`;
    }
    return undefined;
}

/**
 * Monotonic position of a run's status in its lifecycle. Used to reject stale
 * updates (e.g. a `pending` HTTP response arriving after an SSE already
 * advanced the row to `success`). Higher = further along.
 */
export function runPhaseOrder(status: string): number {
    if (status === "pending") return 0;
    if (status === "running") return 1;
    return 2;
}
