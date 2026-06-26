// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { FAILURE_END_REASONS, TRIGGERS, type Trigger } from "@runwisp/common";

export type RunsListSortDirection = "asc" | "desc" | "";

/**
 * The single filter shape shared by the runs list, the filter popover, the
 * SSE-merge source, and the bulk selector. Every field beyond `search` and
 * `sort_direction` is an optional dimension applied server-side; the zero
 * value (`emptyRunFilters`) matches every run.
 *
 * `statuses` is a multi-select set joined to a comma-separated string only at
 * the two wire boundaries (the list query and the bulk selector). Time bounds
 * are absolute RFC3339 instants — presets resolve to `now − delta` at apply
 * time so a live SSE row created after the boundary keeps matching.
 */
export interface RunsListFilters {
    search: string;
    statuses: string[];
    sort_direction: RunsListSortDirection;
    // Optional dimensions explicitly admit `undefined` so a dimension can be
    // cleared by reassignment (`{ ...f, x: undefined }`) under the project's
    // exactOptionalPropertyTypes — a fresh object reference is what re-triggers
    // the parent's fetch effect through several levels of `bind:`.
    task_name?: string | undefined;
    created_after?: string | undefined;
    created_before?: string | undefined;
    triggered_by?: string | undefined;
    // A human exit-code expression — a bare code (`137`), a comparison (`>100`,
    // `<=150`), or a space-separated combination (`>100 <150`). Normalized to an
    // inclusive [min, max] range (`exitCodeRange`) at the wire boundary; the
    // server only ever sees `exit_code_min` / `exit_code_max`.
    exit_code?: string | undefined;
    retries_only?: boolean | undefined;
}

/** A fresh default filter — no dimensions active, newest-first. */
export function emptyRunFilters(): RunsListFilters {
    return { search: "", statuses: [], sort_direction: "desc" };
}

/**
 * Statuses worth an operator's attention: every end reason the retry policy
 * treats as a failure, plus `missed` (a scheduled run the daemon was down
 * for). Backs the one-click preset that serves Prime Directive #1 — nothing
 * silently fails.
 */
export const NEEDS_ATTENTION_STATUSES: readonly string[] = [...FAILURE_END_REASONS, "missed"];

/** True when `statuses` is exactly the needs-attention set (order-insensitive). */
export function isNeedsAttention(statuses: string[]): boolean {
    if (statuses.length !== NEEDS_ATTENTION_STATUSES.length) return false;
    const set = new Set(statuses);
    return NEEDS_ATTENTION_STATUSES.every((s) => set.has(s));
}

/**
 * Outcome buckets: the 14 individual run statuses collapsed into five
 * plain-language groups, so the common case is a five-item pick instead of a
 * flat checklist. Each bucket is purely a UI grouping over `statuses` — toggling
 * one adds/removes its members, and the popover's "Advanced" section still
 * exposes the individual statuses for surgical filters (e.g. only `timeout`).
 *
 * Every status appears in exactly one bucket; `Failed` is the needs-attention
 * set (Prime Directive #1). `dot` mirrors that group's color in
 * RUN_STATUS_CONFIG so the bucket reads at a glance.
 */
export interface StatusBucket {
    key: string;
    label: string;
    /** Tailwind background class for the bucket's status dot. */
    dot: string;
    statuses: readonly string[];
}

export const STATUS_BUCKETS: readonly StatusBucket[] = [
    { key: "running", label: "Running", dot: "bg-info-surface", statuses: ["pending", "running"] },
    { key: "succeeded", label: "Succeeded", dot: "bg-success-surface", statuses: ["success"] },
    {
        key: "failed",
        label: "Failed",
        dot: "bg-danger-surface",
        statuses: NEEDS_ATTENTION_STATUSES,
    },
    {
        key: "skipped",
        label: "Skipped",
        dot: "bg-on-surface-faint",
        statuses: ["skipped", "dst_skipped", "queue_full"],
    },
    {
        key: "stopped",
        label: "Stopped",
        dot: "bg-warning-surface",
        statuses: ["stopped", "daemon_stopped"],
    },
];

export type BucketState = "on" | "partial" | "off";

/** Whether none, some, or all of a bucket's statuses are currently selected. */
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
 * `task` only ever applies on the cross-task /runs view — on a single task's
 * page the task name is the page scope (injected at fetch time), never a
 * popover-set filter, so it stays absent from those filters and is not counted.
 */
export type FilterDimension = "status" | "time" | "task" | "triggered_by" | "exit_code" | "retries";

const DIMENSION_ORDER: FilterDimension[] = [
    "status",
    "time",
    "task",
    "triggered_by",
    "exit_code",
    "retries",
];

/** Whether a single dimension is currently constraining the result set. */
export function dimensionActive(f: RunsListFilters, dim: FilterDimension): boolean {
    switch (dim) {
        case "status":
            return f.statuses.length > 0;
        case "time":
            return Boolean(f.created_after) || Boolean(f.created_before);
        case "task":
            return Boolean(f.task_name);
        case "triggered_by":
            return Boolean(f.triggered_by);
        case "exit_code":
            return exitCodeRangeActive(f.exit_code);
        case "retries":
            return Boolean(f.retries_only);
    }
}

/** The active dimensions, in display order — drives the chip row. */
export function activeDimensions(f: RunsListFilters): FilterDimension[] {
    return DIMENSION_ORDER.filter((dim) => dimensionActive(f, dim));
}

/** How many dimensions are active — the count badge on the Filter button. */
export function activeFilterCount(f: RunsListFilters): number {
    return activeDimensions(f).length;
}

/** Reset one dimension, leaving the others (and search/sort) untouched. */
export function clearDimension(f: RunsListFilters, dim: FilterDimension): RunsListFilters {
    switch (dim) {
        case "status":
            return { ...f, statuses: [] };
        case "time":
            return { ...f, created_after: undefined, created_before: undefined };
        case "task":
            return { ...f, task_name: undefined };
        case "triggered_by":
            return { ...f, triggered_by: undefined };
        case "exit_code":
            return { ...f, exit_code: undefined };
        case "retries":
            return { ...f, retries_only: undefined };
    }
}

/**
 * Clear every popover dimension at once, preserving the header search and the
 * sort direction (those are separate toolbar controls, not popover state).
 */
export function clearPopoverFilters(f: RunsListFilters): RunsListFilters {
    return {
        ...f,
        statuses: [],
        created_after: undefined,
        created_before: undefined,
        task_name: undefined,
        triggered_by: undefined,
        exit_code: undefined,
        retries_only: undefined,
    };
}

/** Human label for a status value, e.g. `log_overflow` → "Log overflow". */
export function humanizeStatus(status: string): string {
    if (!status) return status;
    const spaced = status.replace(/_/g, " ");
    return spaced.charAt(0).toUpperCase() + spaced.slice(1);
}

/**
 * Descriptive label for a run's trigger source — fuller than the one-word row
 * badge (`formatTriggeredByLabel`), for the filter dropdown and chip where the
 * extra words disambiguate (e.g. "Manual (UI or API)" vs a bare "API").
 *
 * Note `cron` covers both on-time schedule firings and catch-up for missed
 * runs, and `startup` is specifically `run_on_start` (not catch-up).
 */
export function triggerDescription(trigger: string): string {
    switch (trigger) {
        case "cron":
            return "Scheduled (cron)";
        case "api":
            return "Manual (UI or API)";
        case "cloud":
            return "Control plane";
        case "service":
            return "Service auto-start";
        case "startup":
            return "On daemon start";
        default:
            return humanizeStatus(trigger);
    }
}

/**
 * The triggers offered in the filter dropdown. `cloud` is intentionally
 * excluded for now — control-plane runs still carry the `cloud` trigger, but
 * it isn't a selectable filter dimension.
 */
export const FILTERABLE_TRIGGERS: readonly Trigger[] = TRIGGERS.filter((t) => t !== "cloud");

/** Chip label for the status dimension — names a whole bucket when it matches. */
export function statusChipLabel(statuses: string[]): string {
    const bucket = exactBucket(statuses);
    if (bucket) return bucket.label;
    const [only] = statuses;
    if (statuses.length === 1 && only !== undefined) return humanizeStatus(only);
    return `${String(statuses.length)} statuses`;
}

// --- Time helpers --------------------------------------------------------
//
// A bare calendar day (`YYYY-MM-DD` from a native date input) maps to a local
// instant: the "from" edge is that day's 00:00, the "to" edge is its end
// (23:59:59.999) so picking the same day for both bounds — or the one-click
// "on day" shortcut — captures the whole day and round-trips back to the same
// date in the inputs. Bounds are stored as absolute RFC3339 (UTC) instants, the
// shape the list query and SSE-merge filter already expect.

/** Parse `YYYY-MM-DD` into the local Date for that day, or null if malformed. */
function localDay(dateStr: string): Date | null {
    const parts = dateStr.split("-");
    if (parts.length !== 3) return null;
    const [y, m, d] = parts.map((p) => Number(p));
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

// --- Exit-code expression ------------------------------------------------
//
// The popover takes a free-form exit-code expression and normalizes it to an
// inclusive integer [min, max] range — the shape the server gates on. Because
// exit codes are integers, a strict comparison collapses to an inclusive
// bound: `>100` is min 101, `<150` is max 149. Tokens combine (`>100 <150`),
// and a bare number is an exact match (min = max = n).

export interface ExitCodeRange {
    min?: number;
    max?: number;
}

const EXIT_CODE_TOKEN = /^(>=|<=|>|<)?(-?\d+)$/;

/**
 * Parse an exit-code expression into an inclusive [min, max] range. Returns
 * `valid: false` if any token is unparseable, so the UI can flag a typo
 * instead of silently applying half of it. An empty expression is valid with
 * no bounds (no constraint).
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

/** True when the expression resolves to at least one bound (a real filter). */
export function exitCodeRangeActive(expr: string | undefined): boolean {
    const { min, max } = exitCodeRange(expr);
    return min !== undefined || max !== undefined;
}

/** True when the expression is empty or fully parseable — drives input validation. */
export function isExitCodeExprValid(expr: string): boolean {
    return parseExitCodeRange(expr).valid;
}

/** Chip label for an active exit-code filter, e.g. `Exit >100 <150`. */
export function exitCodeChipLabel(expr: string | undefined): string {
    return `Exit ${(expr ?? "").trim()}`;
}
