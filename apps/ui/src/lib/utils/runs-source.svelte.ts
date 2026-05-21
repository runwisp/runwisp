// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run } from "@runwisp/common";
import { runPhaseOrder } from "@runwisp/ui";
import { runsApi, tasksApi } from "$lib/api";

const PAGE_SIZE = 50;

export type RunsSortDirection = "asc" | "desc" | "";

export interface RunsFilters {
    search: string;
    status: string;
    sort_direction: RunsSortDirection;
    task_name?: string;
}

export interface RunsSource {
    readonly items: Run[];
    readonly total: number;
    readonly loading: boolean;
    readonly error: Error | null;
    readonly filters: RunsFilters | null;
    readonly done: boolean;
    setFilters(next: RunsFilters): void;
    loadMore(): void;
    upsert(run: Run): void;
    remove(runId: string): void;
}

type RunsQuery = NonNullable<Parameters<typeof runsApi.getAll>[0]>;

function filtersEqual(a: RunsFilters | null, b: RunsFilters): boolean {
    if (!a) return false;
    return (
        a.search === b.search &&
        a.status === b.status &&
        a.sort_direction === b.sort_direction &&
        a.task_name === b.task_name
    );
}

function buildQuery(offset: number, f: RunsFilters): RunsQuery {
    const params: RunsQuery = { limit: PAGE_SIZE, offset };
    const search = f.search.trim();
    if (search) params.search = search;
    if (f.status && f.status !== "all") params.status = f.status;
    if (f.sort_direction) params.sort_direction = f.sort_direction;
    return params;
}

function matchesFilters(run: Run, f: RunsFilters): boolean {
    if (f.task_name && run.task_name !== f.task_name) return false;
    if (f.status && f.status !== "all" && run.status !== f.status) return false;
    const query = f.search.trim().toLowerCase();
    if (!query) return true;
    return run.id.toLowerCase().includes(query) || run.task_name.toLowerCase().includes(query);
}

/** True when filters use the server default sort (created_at DESC). */
function isCreatedAtDesc(f: RunsFilters): boolean {
    return f.sort_direction !== "asc";
}

export function createRunsSource(): RunsSource {
    let items = $state<Run[]>([]);
    let total = $state(0);
    let loading = $state(false);
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
            const res = f.task_name
                ? await tasksApi.getRuns(f.task_name, query)
                : await runsApi.getAll(query);
            if (token !== fetchToken) return;
            items = replace ? res.runs : [...items, ...res.runs];
            total = res.total;
            error = null;
        } catch (err) {
            if (token !== fetchToken) return;
            error = err instanceof Error ? err : new Error(String(err));
        } finally {
            if (token === fetchToken) loading = false;
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
            const ts = Date.parse(run.created_at);
            const insertAt = items.findIndex((r) => Date.parse(r.created_at) < ts);
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
        if (idx !== -1) replaceExisting(idx, run, f);
        else insertNew(run, f);
    }

    function remove(runId: string): void {
        const idx = items.findIndex((r) => r.id === runId);
        if (idx !== -1) {
            const next = [...items];
            next.splice(idx, 1);
            items = next;
        }
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
        upsert,
        remove,
    };
}
