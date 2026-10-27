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
 * "CPU 1m 52s · RAM 48 MB" for a run whose resources were measured (shell
 * runs only), else undefined. CPU is the CPU time the run used, RAM the most
 * memory it held.
 */
export function runUsageLabel(run: Pick<Run, "peakMemoryBytes" | "cpuTimeMs">): string | undefined {
    const parts: string[] = [];
    if (run.cpuTimeMs !== undefined) parts.push("CPU " + formatDuration(run.cpuTimeMs));
    if (run.peakMemoryBytes !== undefined) parts.push("RAM " + formatBytes(run.peakMemoryBytes));
    return parts.length > 0 ? parts.join(" · ") : undefined;
}

/** CPU time (ms) and peak memory (bytes) of one run, or of a task's typical run. */
export interface RunUsageAmounts {
    cpu: number;
    ram: number;
}

/**
 * How a task's runs compare: `max` is its largest run (the full length of a
 * usage bar), `usual` the median run (what "normal for this task" means).
 */
export interface RunUsageScale {
    max: RunUsageAmounts;
    usual: RunUsageAmounts;
}

/** A finished run's measured usage, or undefined when it wasn't measured. */
export function runUsageAmounts(
    run: Pick<Run, "status" | "cpuTimeMs" | "peakMemoryBytes">,
): RunUsageAmounts | undefined {
    if (run.status !== "ended") return undefined;
    if (run.cpuTimeMs === undefined || run.peakMemoryBytes === undefined) return undefined;
    return { cpu: run.cpuTimeMs, ram: run.peakMemoryBytes };
}

function median(values: number[]): number {
    const sorted = values.toSorted((a, b) => a - b);
    return sorted[Math.floor(sorted.length / 2)] ?? 0;
}

/** The scale for a task's usage bars, over the measured runs given. */
export function runUsageScale(
    runs: readonly Pick<Run, "status" | "cpuTimeMs" | "peakMemoryBytes">[],
): RunUsageScale | undefined {
    const amounts = runs.flatMap((run) => runUsageAmounts(run) ?? []);
    if (amounts.length === 0) return undefined;
    const cpu = amounts.map((a) => a.cpu);
    const ram = amounts.map((a) => a.ram);
    return {
        max: { cpu: Math.max(...cpu), ram: Math.max(...ram) },
        usual: { cpu: median(cpu), ram: median(ram) },
    };
}

/** A finished run's wall time in ms, or undefined while it is still going. */
export function runDurationMs(
    run: Pick<Run, "status" | "startedAt" | "endedAt">,
): number | undefined {
    if (run.status !== "ended" || !run.startedAt || !run.endedAt) return undefined;
    return new Date(run.endedAt).getTime() - new Date(run.startedAt).getTime();
}

/** The median wall time in ms over the finished runs given, the baseline a slow run is judged against. */
export function usualDuration(
    runs: readonly Pick<Run, "status" | "startedAt" | "endedAt">[],
): number | undefined {
    const durations = runs.flatMap((run) => runDurationMs(run) ?? []);
    return durations.length > 0 ? median(durations) : undefined;
}

/**
 * The least a usual run counts as using, so a tiny or zero baseline (a mostly
 * idle task's median CPU time is often 0) doesn't hide a run that burns real
 * CPU or memory, nor flag 3ms against 1ms. CPU time and duration in ms, memory
 * in bytes. Duration's floor is higher since wall time also carries network
 * and scheduling jitter. Mirrors the TUI's uikit.UsageFloor*.
 */
const USAGE_FLOOR = { cpu: 1000, ram: 16 * 1024 * 1024, duration: 5000 };

/**
 * How unusual a run's amount is for its task: 0 about as usual, 1 at 1.5×
 * the usual run, 2 at twice it or more. A task that always uses the same
 * amount stays at 0, however large that amount is. The usual run counts as
 * at least USAGE_FLOOR for its kind.
 */
export function usageLevel(
    value: number,
    usual: number,
    kind: keyof typeof USAGE_FLOOR,
): 0 | 1 | 2 {
    const baseline = Math.max(usual, USAGE_FLOOR[kind]);
    if (baseline <= 0) return 0;
    const ratio = value / baseline;
    if (ratio >= 2) return 2;
    if (ratio >= 1.5) return 1;
    return 0;
}

/** Lines a finished run printed on stdout and stderr together, or undefined without counts. */
export function runOutputLines(
    run: Pick<Run, "status" | "stdoutLines" | "stderrLines">,
): number | undefined {
    if (run.status !== "ended") return undefined;
    if (run.stdoutLines === undefined || run.stderrLines === undefined) return undefined;
    return run.stdoutLines + run.stderrLines;
}

/** The range of output a task's runs usually print, in lines: its 10th and 90th percentile. */
export interface OutputBand {
    p10: number;
    p90: number;
}

/** How many finished runs a task needs before a run is judged against its band. */
const OUTPUT_BAND_MIN_RUNS = 10;
/** The fewest lines a run must be past the band's edge, so 3 against a usual 1 isn't flagged. */
const OUTPUT_BAND_SLACK = 5;

/** The p-th percentile of sorted values, by the nearest-rank method. */
function nearestRank(sorted: number[], p: number): number {
    const rank = Math.max(Math.ceil((p * sorted.length) / 100), 1);
    return sorted[rank - 1] ?? 0;
}

/**
 * The output band over the finished runs given, or undefined while fewer than
 * OUTPUT_BAND_MIN_RUNS have line counts. Mirrors the TUI's uikit.UsualOutput.
 */
export function outputBand(
    runs: readonly Pick<Run, "status" | "stdoutLines" | "stderrLines">[],
): OutputBand | undefined {
    const lines = runs.flatMap((run) => runOutputLines(run) ?? []);
    if (lines.length < OUTPUT_BAND_MIN_RUNS) return undefined;
    const sorted = lines.toSorted((a, b) => a - b);
    return { p10: nearestRank(sorted, 10), p90: nearestRank(sorted, 90) };
}

/**
 * Whether a line count is clearly outside the band: more than twice its 90th
 * percentile or under half its 10th, and at least OUTPUT_BAND_SLACK lines past
 * it. A task that is silent nearly every time is flagged by any output at all.
 */
export function outputUnusual(lines: number, band: OutputBand): boolean {
    if (band.p90 === 0) return lines > 0;
    const high = lines > 2 * band.p90 && lines - band.p90 >= OUTPUT_BAND_SLACK;
    const low = 2 * lines < band.p10 && band.p10 - lines >= OUTPUT_BAND_SLACK;
    return high || low;
}

/**
 * How a task's runs compare on output: `max` is its chattiest run (the full
 * length of an output bar), `band` its usual range, undefined until enough
 * runs have counts. Undefined when no run has counts yet.
 */
export interface OutputScale {
    max: number;
    band: OutputBand | undefined;
}

/** The scale for a task's output bars, over the finished runs given. */
export function runOutputScale(
    runs: readonly Pick<Run, "status" | "stdoutLines" | "stderrLines">[],
): OutputScale | undefined {
    const lines = runs.flatMap((run) => runOutputLines(run) ?? []);
    if (lines.length === 0) return undefined;
    return { max: Math.max(...lines), band: outputBand(runs) };
}

const compactNumber = new Intl.NumberFormat("en", {
    notation: "compact",
    maximumFractionDigits: 1,
});

/** "1 line", "37 lines", "1.2K lines". */
export function formatLines(lines: number): string {
    return lines === 1 ? "1 line" : `${compactNumber.format(lines)} lines`;
}

/**
 * How much a run printed: "no output", "12 lines", "12 lines · 3 on stderr",
 * or undefined for a run without counts.
 */
export function runOutputLabel(
    run: Pick<Run, "status" | "stdoutLines" | "stderrLines">,
): string | undefined {
    const lines = runOutputLines(run);
    if (lines === undefined) return undefined;
    if (lines === 0) return "no output";
    const stderr = run.stderrLines ?? 0;
    if (stderr === 0) return formatLines(lines);
    if (stderr === lines) return `${formatLines(lines)} · all on stderr`;
    return `${formatLines(lines)} · ${String(stderr)} on stderr`;
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
