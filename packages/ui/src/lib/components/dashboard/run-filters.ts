// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { TRIGGERS, type Trigger } from "@runwisp/common";
import { formatCalendarDate, formatDateTime } from "../../utils/format.js";
import { TRIGGER_LABELS } from "./run-helpers.js";
import { RUN_STATUS_CONFIG } from "./status-config.js";

/**
 * The filter shared by the runs list, the filter popover, the SSE-merge source
 * and the bulk selector. Time bounds are absolute RFC3339 instants, so a live
 * SSE row created after a preset was applied keeps matching.
 */
export interface RunsListFilters {
    search: string;
    statuses: string[];
    sortDirection: "asc" | "desc" | "";
    // Optional dimensions explicitly admit `undefined` so a dimension can be
    // cleared by reassignment (`{ ...f, x: undefined }`) under the project's
    // exactOptionalPropertyTypes, a fresh object reference is what re-triggers
    // the parent's fetch effect through several levels of `bind:`.
    taskName?: string | undefined;
    createdAfter?: string | undefined;
    createdBefore?: string | undefined;
    triggeredBy?: string | undefined;
    // An exit-code expression such as `137`, `>100` or `>100 <150`; see
    // parseExitCodeRange.
    exitCode?: string | undefined;
    retriesOnly?: boolean | undefined;
}

/** A fresh default filter, no dimensions active, newest-first. */
export function emptyRunFilters(): RunsListFilters {
    return { search: "", statuses: [], sortDirection: "desc" };
}

/**
 * Reserved `status` token for the "Failed" filter. The daemon matches it on each
 * run's `isFailure` bit (per-task `failures` policy) instead of a fixed
 * end-reason list. Must match `failureStatusToken` in the daemon.
 */
export const FAILURE_STATUS_TOKEN = "failure";

/**
 * Outcome buckets: a UI grouping of the individual run statuses into five
 * plain-language picks. The popover's "Advanced" section still exposes the
 * individual statuses.
 */
export interface StatusBucket {
    key: string;
    label: string;
    dot: string;
    statuses: readonly string[];
}

export const STATUS_BUCKETS: readonly StatusBucket[] = [
    {
        key: "running",
        label: "Running",
        dot: RUN_STATUS_CONFIG.running.solidDot,
        statuses: ["pending", "running"],
    },
    {
        key: "succeeded",
        label: "Succeeded",
        dot: RUN_STATUS_CONFIG.succeeded.solidDot,
        statuses: ["succeeded"],
    },
    {
        key: "failed",
        label: "Failed",
        dot: RUN_STATUS_CONFIG.failed.solidDot,
        statuses: [FAILURE_STATUS_TOKEN],
    },
    {
        key: "skipped",
        label: "Skipped",
        dot: RUN_STATUS_CONFIG.skipped.solidDot,
        statuses: ["skipped", "dst_skipped", "queue_full"],
    },
    {
        key: "stopped",
        label: "Stopped",
        dot: RUN_STATUS_CONFIG.stopped.solidDot,
        statuses: ["stopped", "daemon_stopped"],
    },
];

export type BucketState = "on" | "partial" | "off";

export function bucketState(statuses: string[], bucket: StatusBucket): BucketState {
    const set = new Set(statuses);
    const present = bucket.statuses.filter((s) => set.has(s)).length;
    if (present === 0) return "off";
    if (present === bucket.statuses.length) return "on";
    return "partial";
}

/** Toggle a whole bucket: fully selected → clear it; otherwise select all of it. */
export function toggleBucket(f: RunsListFilters, bucket: StatusBucket): RunsListFilters {
    const members = new Set(bucket.statuses);
    if (bucketState(f.statuses, bucket) === "on") {
        return { ...f, statuses: f.statuses.filter((s) => !members.has(s)) };
    }
    return { ...f, statuses: [...new Set([...f.statuses, ...bucket.statuses])] };
}

/** The bucket the selection matches exactly (all of it, nothing else), if any. */
function exactBucket(statuses: string[]): StatusBucket | undefined {
    return STATUS_BUCKETS.find(
        (b) => b.statuses.length === statuses.length && bucketState(statuses, b) === "on",
    );
}

/**
 * The popover-managed dimensions, in display (most→least useful) order.
 * `task` only ever applies on the cross-task /runs view, on a single task's
 * page the task name is the page scope (injected at fetch time), never a
 * popover-set filter, so it stays absent from those filters and is not counted.
 */
export type FilterDimension = "status" | "time" | "task" | "triggeredBy" | "exitCode" | "retries";

const DIMENSION_ORDER: FilterDimension[] = [
    "status",
    "time",
    "task",
    "triggeredBy",
    "exitCode",
    "retries",
];

export function dimensionActive(f: RunsListFilters, dim: FilterDimension): boolean {
    switch (dim) {
        case "status":
            return f.statuses.length > 0;
        case "time":
            return Boolean(f.createdAfter) || Boolean(f.createdBefore);
        case "task":
            return Boolean(f.taskName);
        case "triggeredBy":
            return Boolean(f.triggeredBy);
        case "exitCode":
            return exitCodeRangeActive(f.exitCode);
        case "retries":
            return Boolean(f.retriesOnly);
    }
}

/** The active dimensions, in display order, drives the chip row. */
export function activeDimensions(f: RunsListFilters): FilterDimension[] {
    return DIMENSION_ORDER.filter((dim) => dimensionActive(f, dim));
}

/** How many dimensions are active, the count badge on the Filter button. */
export function activeFilterCount(f: RunsListFilters): number {
    return activeDimensions(f).length;
}

export function clearDimension(f: RunsListFilters, dim: FilterDimension): RunsListFilters {
    switch (dim) {
        case "status":
            return { ...f, statuses: [] };
        case "time":
            return { ...f, createdAfter: undefined, createdBefore: undefined };
        case "task":
            return { ...f, taskName: undefined };
        case "triggeredBy":
            return { ...f, triggeredBy: undefined };
        case "exitCode":
            return { ...f, exitCode: undefined };
        case "retries":
            return { ...f, retriesOnly: undefined };
    }
}

/** Human label for a status value, e.g. `log_overflow` → "Log overflow". */
export function humanizeStatus(status: string): string {
    if (!status) return status;
    const spaced = status.replaceAll("_", " ");
    return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

/** Descriptive label for a run's trigger source (see {@link TRIGGER_LABELS}). */
export function triggerDescription(trigger: string): string {
    const known = TRIGGERS.find((t) => t === trigger);
    return known ? TRIGGER_LABELS[known].long : humanizeStatus(trigger);
}

/**
 * The triggers offered in the filter dropdown. `station` is intentionally
 * excluded for now, control-plane runs still carry the `station` trigger, but
 * it isn't a selectable filter dimension.
 */
export const FILTERABLE_TRIGGERS: readonly Trigger[] = TRIGGERS.filter((t) => t !== "station");

/** Chip label for the status dimension, names a whole bucket when it matches. */
export function statusChipLabel(statuses: string[]): string {
    const bucket = exactBucket(statuses);
    if (bucket) return bucket.label;
    const [only] = statuses;
    if (statuses.length === 1 && only !== undefined) return humanizeStatus(only);
    return `${String(statuses.length)} statuses`;
}

/** Parse `YYYY-MM-DD` into the local Date for that day, or null if malformed. */
function localDay(dateStr: string): Date | null {
    const parts = dateStr.split("-");
    if (parts.length !== 3) return null;
    const [y, m, d] = parts.map(Number);
    if (y === undefined || m === undefined || d === undefined) return null;
    if (!Number.isInteger(y) || !Number.isInteger(m) || !Number.isInteger(d)) return null;
    const date = new Date(y, m - 1, d);
    return Number.isNaN(date.getTime()) ? null : date;
}

/** `YYYY-MM-DD` → that day's 00:00 local, as an RFC3339 instant. */
export function dayStartIso(dateStr: string): string | undefined {
    const d = localDay(dateStr);
    return d ? d.toISOString() : undefined;
}

/** `YYYY-MM-DD` → that day's last millisecond local, as an RFC3339 instant. */
export function dayEndIso(dateStr: string): string | undefined {
    const d = localDay(dateStr);
    if (!d) return undefined;
    return new Date(d.getFullYear(), d.getMonth(), d.getDate(), 23, 59, 59, 999).toISOString();
}

/** An RFC3339 instant → the local `YYYY-MM-DD` it falls on (for date inputs). */
export function isoToDayInput(iso: string): string {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return "";
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${String(d.getFullYear())}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * True when the bounds describe exactly one calendar day (its 00:00 → end of
 * day). Picking the same date for both From and To produces such a range, so
 * the chip can read "On <date>" instead of a from–to pair.
 */
export function isWholeDay(after?: string, before?: string): boolean {
    if (!after || !before) return false;
    const a = isoToDayInput(after);
    return (
        a !== "" &&
        a === isoToDayInput(before) &&
        new Date(after).getTime() < new Date(before).getTime()
    );
}

export interface ExitCodeRange {
    min?: number;
    max?: number;
}

const EXIT_CODE_TOKEN = /^(>=|<=|>|<)?(-?\d+)$/;

/**
 * Parse an exit-code expression into the inclusive integer range the server
 * filters on: `>100` is min 101, `<150` is max 149, a bare number is an exact
 * match, and tokens combine. Any unparseable token makes the whole expression
 * invalid so the UI can flag the typo. An empty expression is valid and unbounded.
 */
export function parseExitCodeRange(expr: string): { range: ExitCodeRange; valid: boolean } {
    const range: ExitCodeRange = {};
    const tokens = expr.trim().split(/\s+/).filter(Boolean);
    for (const tok of tokens) {
        const m = EXIT_CODE_TOKEN.exec(tok);
        if (!m) return { range: {}, valid: false };
        const numStr = m[2];
        if (numStr === undefined) return { range: {}, valid: false };
        const n = Number.parseInt(numStr, 10);
        switch (m[1]) {
            case ">":
                range.min = n + 1;
                break;
            case ">=":
                range.min = n;
                break;
            case "<":
                range.max = n - 1;
                break;
            case "<=":
                range.max = n;
                break;
            default:
                range.min = n;
                range.max = n;
        }
    }
    return { range, valid: true };
}

/** The resolved range for an expression, or an empty (unbounded) range if invalid. */
export function exitCodeRange(expr: string | undefined): ExitCodeRange {
    if (!expr) return {};
    const { range, valid } = parseExitCodeRange(expr);
    return valid ? range : {};
}

export function exitCodeRangeActive(expr: string | undefined): boolean {
    const { min, max } = exitCodeRange(expr);
    return min !== undefined || max !== undefined;
}

/** True when the expression is empty or fully parseable, drives input validation. */
export function isExitCodeExprValid(expr: string): boolean {
    return parseExitCodeRange(expr).valid;
}

/**
 * The server-side filter fields for a filter state. The list query and the
 * bulk selector both build on it, so "select all matching" targets exactly the
 * rows on screen. Dimensions that are off are omitted.
 */
export interface RunFilterParams {
    taskName?: string;
    search?: string;
    status?: string;
    createdAfter?: string;
    createdBefore?: string;
    triggeredBy?: string;
    exitCodeMin?: number;
    exitCodeMax?: number;
    retriesOnly?: true;
}

export function runFilterParams(f: RunsListFilters): RunFilterParams {
    const params: RunFilterParams = {};
    if (f.taskName) params.taskName = f.taskName;
    const search = f.search.trim();
    if (search) params.search = search;
    if (f.statuses.length > 0) params.status = f.statuses.join(",");
    if (f.createdAfter) params.createdAfter = f.createdAfter;
    if (f.createdBefore) params.createdBefore = f.createdBefore;
    if (f.triggeredBy) params.triggeredBy = f.triggeredBy;
    const exit = exitCodeRange(f.exitCode);
    if (exit.min !== undefined) params.exitCodeMin = exit.min;
    if (exit.max !== undefined) params.exitCodeMax = exit.max;
    if (f.retriesOnly === true) params.retriesOnly = true;
    return params;
}

/** Chip label for an active exit-code filter, e.g. `Exit >100 <150`. */
export function exitCodeChipLabel(expr: string | undefined): string {
    return `Exit ${(expr ?? "").trim()}`;
}

function timeChipLabel(f: RunsListFilters): string {
    const { createdAfter: after, createdBefore: before } = f;
    if (isWholeDay(after, before) && after) return `On ${formatCalendarDate(after)}`;
    if (after && before) return `${formatDateTime(after)} – ${formatDateTime(before)}`;
    if (after) return `Since ${formatDateTime(after)}`;
    if (before) return `Before ${formatDateTime(before)}`;
    return "";
}

/** Label of the chip shown for an active filter dimension. */
export function filterChipLabel(f: RunsListFilters, dim: FilterDimension): string {
    switch (dim) {
        case "status":
            return statusChipLabel(f.statuses);
        case "time":
            return timeChipLabel(f);
        case "task":
            return f.taskName ?? "";
        case "triggeredBy":
            return "Trigger: " + triggerDescription(f.triggeredBy ?? "");
        case "exitCode":
            return exitCodeChipLabel(f.exitCode);
        case "retries":
            return "Retries only";
    }
}
