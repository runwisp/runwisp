// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { APIPaths, Run, RunStatus, Trigger } from "@runwisp/common";
import { formatBytes, formatDuration } from "../../utils/format.js";

export interface RunVerdict {
    /** Verb phrase for the outcome, ending in its preposition when `timed`. */
    verb: string;
    /** Whether a duration follows the verb; false when nothing ran yet. */
    timed: boolean;
}

// The run's outcome as a verb phrase, read as one sentence: "succeeded in 933ms".
export const RUN_VERDICTS: Record<RunStatus, RunVerdict> = {
    succeeded: { verb: "succeeded in", timed: true },
    failed: { verb: "failed after", timed: true },
    crashed: { verb: "crashed after", timed: true },
    timeout: { verb: "timed out after", timed: true },
    stopped: { verb: "stopped after", timed: true },
    daemon_stopped: { verb: "cut short after", timed: true },
    log_overflow: { verb: "killed after", timed: true },
    start_failed: { verb: "gave up after", timed: true },
    unhealthy: { verb: "killed as unhealthy after", timed: true },
    ended: { verb: "ended after", timed: true },
    running: { verb: "running for", timed: true },
    pending: { verb: "queued", timed: false },
    missed: { verb: "never ran", timed: false },
    skipped: { verb: "skipped", timed: false },
    dst_skipped: { verb: "skipped", timed: false },
    queue_full: { verb: "skipped", timed: false },
};

export interface RunEndMarker {
    label: string;
    tone: "muted" | "warn";
}

// The line closing a run's console output; only abnormal ends are worth a warning tone.
const RUN_END_MARKERS: Partial<Record<RunStatus, RunEndMarker>> = {
    stopped: { label: "run stopped by operator", tone: "warn" },
    daemon_stopped: { label: "daemon stopped mid-run", tone: "warn" },
    timeout: { label: "run timed out", tone: "warn" },
};
const DEFAULT_END_MARKER: RunEndMarker = { label: "end of output", tone: "muted" };

export function runEndMarker(status: RunStatus): RunEndMarker {
    return RUN_END_MARKERS[status] ?? DEFAULT_END_MARKER;
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
 * How long a run waited between `createdAt` and `startedAt` (jitter or queue
 * wait), or undefined when it started within a second.
 */
export function runStartDelay(run: Pick<Run, "createdAt" | "startedAt">): string | undefined {
    if (!run.startedAt) return undefined;
    const delay = new Date(run.startedAt).getTime() - new Date(run.createdAt).getTime();
    if (delay < 1000) return undefined;
    return formatDuration(delay);
}

/** 1-based `#N` suffix for a multi-instance service run, else "". */
export function instanceSuffix(instanceIndex: number, instanceCount: number): string {
    if (instanceCount > 1) {
        return `#${String(instanceIndex + 1)}`;
    }
    return "";
}

// Typed against the generated paths so a route rename fails the build.
const RAW_LOG_PATH: keyof APIPaths = "/api/runs/{runId}/log/raw";

/** The full log of a run as one plain-text download. */
export function runLogDownloadUrl(runId: string): string {
    return RAW_LOG_PATH.replace("{runId}", encodeURIComponent(runId));
}

/** Labels per trigger source: `short` for the row badge, `long` for descriptions. */
export const TRIGGER_LABELS: Record<Trigger, { short: string; long: string }> = {
    cron: { short: "Cron", long: "Scheduled (cron)" },
    api: { short: "API", long: "REST API" },
    ui: { short: "UI", long: "UI" },
    cli: { short: "CLI", long: "CLI" },
    station: { short: "Station", long: "Control plane" },
    service: { short: "Service", long: "Service auto-start" },
    startup: { short: "Startup", long: "On daemon start" },
    hook: { short: "Hook", long: "Hook" },
};

/** Human label for why a run fired (the `triggeredBy` source). */
export function formatTriggeredByLabel(triggeredBy: Run["triggeredBy"]): string {
    return TRIGGER_LABELS[triggeredBy].short;
}

/** "retry #N" when the run re-attempts an earlier one, else undefined. */
export function runRetryLabel(run: Pick<Run, "retryAttempt" | "retryOfRunId">): string | undefined {
    if (run.retryAttempt > 0 || run.retryOfRunId) {
        return `retry #${String(run.retryAttempt)}`;
    }
    return undefined;
}

/**
 * "peak 48 MB · CPU 1s" for a run whose resources were measured (shell runs
 * only), else undefined.
 */
export function runUsageLabel(run: Pick<Run, "peakMemoryBytes" | "cpuTimeMs">): string | undefined {
    const parts: string[] = [];
    if (run.peakMemoryBytes !== undefined) parts.push("peak " + formatBytes(run.peakMemoryBytes));
    if (run.cpuTimeMs !== undefined) parts.push("CPU " + formatDuration(run.cpuTimeMs));
    return parts.length > 0 ? parts.join(" · ") : undefined;
}

/**
 * Monotonic position of a run's status in its lifecycle. Used to reject stale
 * updates (e.g. a `pending` HTTP response arriving after an SSE already
 * advanced the row to `succeeded`). Higher = further along.
 */
export function runPhaseOrder(status: string): number {
    if (status === "pending") return 0;
    if (status === "running") return 1;
    return 2;
}

/** Right-hand mono readout of a run row: live / queued / exit N / duration. */
export function runRowReadout(
    run: Pick<Run, "status" | "exitCode" | "startedAt" | "endedAt">,
    displayed: RunStatus,
): string {
    if (run.status === "running") return "live";
    if (run.status === "pending") return "queued";
    if (displayed === "failed" || displayed === "crashed") return "exit " + String(run.exitCode);
    return runDuration(run) ?? "—";
}

export interface HighlightParts {
    before: string;
    match: string;
    after: string;
}

/**
 * Split an output line into [before, match, after] around the first
 * occurrence of the query, windowed to keep the match in view. Plain strings,
 * so rendering them as text never lets untrusted HTML reach the DOM.
 */
export function highlightParts(text: string, query: string): HighlightParts {
    const q = query.trim();
    const idx = q ? text.toLowerCase().indexOf(q.toLowerCase()) : -1;
    if (idx === -1) return { before: text, match: "", after: "" };
    const start = Math.max(0, idx - 14);
    const lead = start > 0 ? "…" : "";
    const windowed = text.slice(start);
    const fi = windowed.toLowerCase().indexOf(q.toLowerCase());
    return {
        before: lead + windowed.slice(0, fi),
        match: windowed.slice(fi, fi + q.length),
        after: windowed.slice(fi + q.length),
    };
}
