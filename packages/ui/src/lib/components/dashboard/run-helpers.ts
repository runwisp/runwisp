// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run, RunStatus } from "@runwisp/common";
import { formatDuration } from "../../utils/format.js";

export interface RunVerdict {
    /** Verb phrase for the outcome, ending in its preposition when `timed`. */
    verb: string;
    /**
     * Whether the phrase expects a duration after it. False for statuses that
     * never produced one (nothing ran, or nothing has yet), so the caller
     * renders the verb alone rather than "skipped after —".
     */
    timed: boolean;
}

/**
 * The run's outcome as a verb phrase, so a detail view can state it as one
 * sentence ("succeeded in 933ms") instead of a status badge competing with a
 * separate duration readout for the same glance.
 */
const RUN_VERDICTS: Record<RunStatus, RunVerdict> = {
    success: { verb: "succeeded in", timed: true },
    failed: { verb: "failed after", timed: true },
    crashed: { verb: "crashed after", timed: true },
    timeout: { verb: "timed out after", timed: true },
    stopped: { verb: "stopped after", timed: true },
    daemon_stopped: { verb: "cut short after", timed: true },
    log_overflow: { verb: "killed after", timed: true },
    start_failed: { verb: "gave up after", timed: true },
    ended: { verb: "ended after", timed: true },
    running: { verb: "running for", timed: true },
    pending: { verb: "queued", timed: false },
    missed: { verb: "never ran", timed: false },
    skipped: { verb: "skipped", timed: false },
    dst_skipped: { verb: "skipped", timed: false },
    queue_full: { verb: "skipped", timed: false },
};

export function runVerdict(status: RunStatus): RunVerdict {
    return RUN_VERDICTS[status];
}

export function runDuration(
    run: Pick<Run, "startedAt" | "endedAt">,
    now: number = Date.now(),
): string | undefined {
    if (!run.startedAt) return undefined;
    const start = new Date(run.startedAt).getTime();
    const end = run.endedAt ? new Date(run.endedAt).getTime() : now;
    return formatDuration(end - start);
}

/**
 * Gap between when a run was scheduled (`createdAt`, the cron tick) and when
 * it actually started (`startedAt`), formatted — or undefined when the two are
 * within a second of each other. This is the visible face of `jitter`: a
 * jittered run is created at its tick but starts later inside the window, and
 * this is by how much. It also surfaces queue-wait, since a queued run is
 * created when it joins the line and started when the line clears.
 */
export function runStartDelay(run: Pick<Run, "createdAt" | "startedAt">): string | undefined {
    if (!run.startedAt) return undefined;
    const delay = new Date(run.startedAt).getTime() - new Date(run.createdAt).getTime();
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

/** Human label for why a run fired (the `triggeredBy` source). */
export function formatTriggeredByLabel(triggeredBy: Run["triggeredBy"]): string {
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
export function runRetryLabel(run: Pick<Run, "retryAttempt" | "retryOfRunId">): string | undefined {
    if (run.retryAttempt > 0 || run.retryOfRunId) {
        return `retry #${String(run.retryAttempt)}`;
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
