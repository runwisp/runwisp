// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Run, Trigger } from "@runwisp/common";
import { displayStatus, TRIGGERS } from "@runwisp/common";
import {
    runPhaseOrder,
    exitCodeRange,
    type ExitCodeRange,
    type RunsListFilters,
} from "@runwisp/ui";
import { runsApi, tasksApi } from "$lib/api";

const PAGE_SIZE = 50;

export type RunsSortDirection = "asc" | "desc" | "";

// One filter shape across the list, popover, source, and bulk selector — the
// canonical definition lives in @runwisp/ui.
export type RunsFilters = RunsListFilters;

export interface RunsSource {
    readonly items: Run[];
    readonly total: number;
    readonly loading: boolean;
    /** True once the first fetch has settled. Latches on; never resets. */
    readonly loaded: boolean;
    readonly error: Error | null;
    readonly filters: RunsFilters | null;
    readonly done: boolean;
    setFilters(next: RunsFilters): void;
    loadMore(): void;
    /**
     * Re-fetch the first page with the current filters, replacing the list. For
     * resyncing after a genuine SSE reconnect whose gap outlived the server's
     * replay buffer — it delivers true DB state, not a mask over live counts.
     */
    refresh(): void;
    upsert(run: Run): void;
    remove(runId: string): void;
}

type RunsQuery = NonNullable<Parameters<typeof runsApi.getAll>[0]>;

function arraysEqual(a: string[], b: string[]): boolean {
    if (a.length !== b.length) return false;
    return a.every((v, i) => v === b[i]);
}

function filtersEqual(a: RunsFilters | null, b: RunsFilters): boolean {
    if (!a) return false;
    return (
        a.search === b.search &&
        arraysEqual(a.statuses, b.statuses) &&
        a.sortDirection === b.sortDirection &&
        a.taskName === b.taskName &&
        a.createdAfter === b.createdAfter &&
        a.createdBefore === b.createdBefore &&
        a.triggeredBy === b.triggeredBy &&
        a.exitCode === b.exitCode &&
        a.retriesOnly === b.retriesOnly
    );
}

// Narrow a free string to a known Trigger (or undefined) without a cast — the
// query param type is the trigger union, so a plain string won't assign.
function asTrigger(value: string | undefined): Trigger | undefined {
    return TRIGGERS.find((t) => t === value);
}

function buildQuery(offset: number, f: RunsFilters): RunsQuery {
    const params: RunsQuery = { limit: PAGE_SIZE, offset };
    const search = f.search.trim();
    if (search) params.search = search;
    if (f.statuses.length > 0) params.status = f.statuses.join(",");
    if (f.sortDirection) params.sortDirection = f.sortDirection;
    if (f.createdAfter) params.createdAfter = f.createdAfter;
    if (f.createdBefore) params.createdBefore = f.createdBefore;
    const trigger = asTrigger(f.triggeredBy);
    if (trigger) params.triggeredBy = trigger;
    const exit = exitCodeRange(f.exitCode);
    if (exit.min !== undefined) params.exitCodeMin = String(exit.min);
    if (exit.max !== undefined) params.exitCodeMax = String(exit.max);
    if (f.retriesOnly === true) params.retriesOnly = true;
    return params;
}

// Match a run's phase OR its end reason against the set — mirrors the server
// gate. (The old code compared only the phase, so a "failed" filter never
// matched an ended run; this fixes that.)
function matchesStatus(run: Run, statuses: string[]): boolean {
    if (statuses.length === 0) return true;
    const display = displayStatus(run.status, run.endReason);
    return statuses.includes(display) || statuses.includes(run.status);
}

function matchesTimeRange(run: Run, after?: string, before?: string): boolean {
    const created = Date.parse(run.createdAt);
    if (after && created < Date.parse(after)) return false;
    if (before && created > Date.parse(before)) return false;
    return true;
}

// Exit-code gating mirrors the server's inclusive [min, max] range. In-flight
// runs carry exitCode 0, so a positive lower bound drops them — exactly as the
// server query would.
function matchesExitCode(run: Run, range: ExitCodeRange): boolean {
    if (range.min !== undefined && run.exitCode < range.min) return false;
    if (range.max !== undefined && run.exitCode > range.max) return false;
    return true;
}

function matchesSearch(run: Run, search: string): boolean {
    const query = search.trim().toLowerCase();
    if (!query) return true;
    return run.id.toLowerCase().includes(query) || run.taskName.toLowerCase().includes(query);
}

function matchesFilters(run: Run, f: RunsFilters): boolean {
    if (f.taskName && run.taskName !== f.taskName) return false;
    if (!matchesStatus(run, f.statuses)) return false;
    if (!matchesTimeRange(run, f.createdAfter, f.createdBefore)) return false;
    if (f.triggeredBy && run.triggeredBy !== f.triggeredBy) return false;
    if (!matchesExitCode(run, exitCodeRange(f.exitCode))) return false;
    if (f.retriesOnly === true && run.retryAttempt <= 0) return false;
    return matchesSearch(run, f.search);
}

/** True when filters use the server default sort (createdAt DESC). */
function isCreatedAtDesc(f: RunsFilters): boolean {
    return f.sortDirection !== "asc";
}

export function createRunsSource(): RunsSource {
    let items = $state<Run[]>([]);
    let total = $state(0);
    let loading = $state(false);
    let loaded = $state(false);
    let error = $state<Error | null>(null);
    let currentFilters = $state<RunsFilters | null>(null);
    let fetchToken = 0;

    const done = $derived(currentFilters !== null && items.length >= total);

    async function fetchPage(offset: number, replace: boolean): Promise<void> {
        if (!currentFilters) return;
        const f = currentFilters;
        const token = ++fetchToken;
        loading = true;
        try {
            const query = buildQuery(offset, f);
            const res = f.taskName
                ? await tasksApi.getRuns(f.taskName, query)
                : await runsApi.getAll(query);
            if (token !== fetchToken) return;
            items = replace ? res.runs : [...items, ...res.runs];
            total = res.total;
            error = null;
        } catch (err) {
            if (token !== fetchToken) return;
            error = err instanceof Error ? err : new Error(String(err));
        } finally {
            if (token === fetchToken) {
                loading = false;
                loaded = true;
            }
        }
    }

    function setFilters(next: RunsFilters): void {
        if (filtersEqual(currentFilters, next)) return;
        currentFilters = { ...next };
        items = [];
        total = 0;
        void fetchPage(0, true);
    }

    function loadMore(): void {
        if (loading || !currentFilters) return;
        if (items.length >= total) return;
        void fetchPage(items.length, false);
    }

    function refresh(): void {
        if (!currentFilters) return;
        void fetchPage(0, true);
    }

    function replaceExisting(idx: number, run: Run, f: RunsFilters): void {
        const existing = items[idx];
        // Never regress a run's status (e.g. the pending HTTP response from
        // trigger arriving after SSE already advanced the row to success).
        if (existing && runPhaseOrder(run.status) < runPhaseOrder(existing.status)) {
            return;
        }
        const next = [...items];
        if (!matchesFilters(run, f)) {
            next.splice(idx, 1);
            items = next;
            if (total > 0) total -= 1;
            return;
        }
        next[idx] = run;
        items = next;
    }

    function insertNew(run: Run, f: RunsFilters): void {
        if (!matchesFilters(run, f)) return;
        if (isCreatedAtDesc(f)) {
            const ts = Date.parse(run.createdAt);
            const insertAt = items.findIndex((r) => Date.parse(r.createdAt) < ts);
            const next = [...items];
            if (insertAt === -1) next.push(run);
            else next.splice(insertAt, 0, run);
            items = next;
        }
        total += 1;
    }

    function upsert(run: Run): void {
        const f = currentFilters;
        if (!f) return;
        const idx = items.findIndex((r) => r.id === run.id);
        if (idx === -1) insertNew(run, f);
        else replaceExisting(idx, run, f);
    }

    function remove(runId: string): void {
        const idx = items.findIndex((r) => r.id === runId);
        // Only touch total when the run was actually present. The optimistic
        // remove and the server's run.deleted SSE echo both call this for the
        // same id; decrementing on the second (idx === -1) call would drift the
        // count below the true total and can prematurely mark the list "done".
        if (idx === -1) return;
        const next = [...items];
        next.splice(idx, 1);
        items = next;
        if (total > 0) total -= 1;
    }

    return {
        get items() {
            return items;
        },
        get total() {
            return total;
        },
        get loading() {
            return loading;
        },
        get loaded() {
            return loaded;
        },
        get error() {
            return error;
        },
        get filters() {
            return currentFilters;
        },
        get done() {
            return done;
        },
        setFilters,
        loadMore,
        refresh,
        upsert,
        remove,
    };
}
