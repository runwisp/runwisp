// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Run } from "@runwisp/common";

vi.mock("$lib/api", () => ({
    runsApi: { getAll: vi.fn() },
    tasksApi: { getRuns: vi.fn() },
}));

import { runsApi, tasksApi } from "$lib/api";
import { createRunsSource, type RunsFilters, type RunsSource } from "./runs-source.svelte";

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
    return {
        id,
        taskName: "backup-db",
        createdAt: "2026-06-22T12:00:00.000Z",
        status: "ended",
        endReason: "succeeded",
        triggeredBy: "api",
        exitCode: 0,
        instanceIndex: 0,
        retryAttempt: 0,
        ...overrides,
    };
}

const baseFilters = (overrides: Partial<RunsFilters> = {}): RunsFilters => ({
    search: "",
    statuses: [],
    sortDirection: "desc",
    ...overrides,
});

beforeEach(() => {
    vi.clearAllMocks();
});

describe("createRunsSource", () => {
    it("latches `loaded` once the first fetch settles", async () => {
        const src = createRunsSource();
        expect(src.loaded).toBe(false);
        expect(src.loading).toBe(false);

        const runs = [makeRun("a")];
        vi.mocked(runsApi.getAll).mockResolvedValue({ runs, total: 1 });
        src.setFilters(baseFilters());

        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });
        expect(src.items).toEqual(runs);
        expect(src.total).toBe(1);
        expect(src.loading).toBe(false);
        expect(src.error).toBeNull();
    });

    it("fetches a task's own runs through tasksApi when taskName is set", async () => {
        const src = createRunsSource();
        vi.mocked(tasksApi.getRuns).mockResolvedValue({ runs: [], total: 0 });

        src.setFilters(baseFilters({ taskName: "backup-db" }));

        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });
        expect(tasksApi.getRuns).toHaveBeenCalled();
        expect(runsApi.getAll).not.toHaveBeenCalled();
    });

    it("still latches `loaded` and records the error when the fetch rejects", async () => {
        const src = createRunsSource();
        vi.mocked(runsApi.getAll).mockRejectedValue(new Error("offline"));

        src.setFilters(baseFilters());

        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });
        expect(src.error).toBeInstanceOf(Error);
        expect(src.error?.message).toBe("offline");
        expect(src.loading).toBe(false);
    });

    it("refresh() re-fetches the first page with the current filters", async () => {
        const src = createRunsSource();
        vi.mocked(runsApi.getAll).mockResolvedValue({ runs: [makeRun("a")], total: 1 });
        src.setFilters(baseFilters());
        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });

        // The reconnect resync: the DB now holds a corrected set; refresh replaces
        // the list from offset 0 without changing filters.
        vi.mocked(runsApi.getAll).mockResolvedValue({ runs: [makeRun("b")], total: 1 });
        src.refresh();
        await vi.waitFor(() => {
            expect(src.items.map((r) => r.id)).toEqual(["b"]);
        });
        expect(runsApi.getAll).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 0 }));
    });

    it("refresh() is a no-op before any filters are set", () => {
        const src = createRunsSource();
        src.refresh();
        expect(runsApi.getAll).not.toHaveBeenCalled();
    });

    // A loadMore() page fetch captures its offset from items.length at call
    // time. If a live SSE upsert() splices a new row to the front while that
    // fetch is still in flight, every already-loaded run's true server-side
    // rank shifts forward by one — so the in-flight page (computed at the old
    // offset) would re-deliver an already-loaded row if merged blindly.
    // fetchPage detects the drift (items.length no longer matches what it was
    // when the request started) and retries from the corrected offset instead.
    it("retries loadMore's page instead of duplicating a run when a live SSE insert shifts ranks forward", async () => {
        const src = createRunsSource();
        const r5 = makeRun("r5", { createdAt: "2026-06-22T12:05:00.000Z" });
        const r4 = makeRun("r4", { createdAt: "2026-06-22T12:04:00.000Z" });
        const r3 = makeRun("r3", { createdAt: "2026-06-22T12:03:00.000Z" });
        vi.mocked(runsApi.getAll).mockResolvedValueOnce({ runs: [r5, r4, r3], total: 10 });
        src.setFilters(baseFilters());
        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });
        expect(src.items.map((r) => r.id)).toEqual(["r5", "r4", "r3"]);

        // Kick off loadMore() (offset=3) but hold the response back.
        let resolveStale: (v: { runs: Run[]; total: number }) => void = () => {};
        const stale = new Promise<{ runs: Run[]; total: number }>((resolve) => {
            resolveStale = resolve;
        });
        vi.mocked(runsApi.getAll).mockReturnValueOnce(stale);
        src.loadMore();
        expect(runsApi.getAll).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 3 }));

        // A run finishes and its SSE update arrives before the page above
        // resolves — inserted at the front, shifting r5/r4/r3's true rank.
        const fresh = makeRun("fresh", { createdAt: "2026-06-22T12:06:00.000Z" });
        src.upsert(fresh);
        expect(src.items.map((r) => r.id)).toEqual(["fresh", "r5", "r4", "r3"]);

        // The stale page (whatever it contains) must be discarded, never
        // merged — its content is irrelevant to the assertions below.
        const ghost = makeRun("ghost", { createdAt: "2026-06-22T12:03:30.000Z" });
        resolveStale({ runs: [ghost], total: 11 });

        // A retry fires at the corrected offset (4, matching items.length
        // after the insert), and its response is what actually gets merged.
        const r2 = makeRun("r2", { createdAt: "2026-06-22T12:02:00.000Z" });
        vi.mocked(runsApi.getAll).mockResolvedValueOnce({ runs: [r2], total: 11 });
        await vi.waitFor(() => {
            expect(runsApi.getAll).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 4 }));
        });
        await vi.waitFor(() => {
            expect(src.items.map((r) => r.id)).toContain("r2");
        });

        expect(src.items.map((r) => r.id)).toEqual(["fresh", "r5", "r4", "r3", "r2"]);
    });

    // The opposite direction of the same race: a live remove() (the optimistic
    // delete, or its SSE echo) shifts every later run's true server-side rank
    // *back* by one while loadMore()'s page is in flight. Blindly appending
    // that page would silently skip a run entirely — there's no duplicate id
    // to filter out, the row is just gone. The same drift detection that
    // fixes the insert case must also catch this and retry.
    it("retries loadMore's page instead of skipping a run when a live remove shifts ranks back", async () => {
        const src = createRunsSource();
        const r5 = makeRun("r5", { createdAt: "2026-06-22T12:05:00.000Z" });
        const r4 = makeRun("r4", { createdAt: "2026-06-22T12:04:00.000Z" });
        const r3 = makeRun("r3", { createdAt: "2026-06-22T12:03:00.000Z" });
        vi.mocked(runsApi.getAll).mockResolvedValueOnce({ runs: [r5, r4, r3], total: 10 });
        src.setFilters(baseFilters());
        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });

        let resolveStale: (v: { runs: Run[]; total: number }) => void = () => {};
        const stale = new Promise<{ runs: Run[]; total: number }>((resolve) => {
            resolveStale = resolve;
        });
        vi.mocked(runsApi.getAll).mockReturnValueOnce(stale);
        src.loadMore();
        expect(runsApi.getAll).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 3 }));

        // r4 is deleted (the SSE echo of a real deletion) while that page is
        // still in flight.
        src.remove("r4");
        expect(src.items.map((r) => r.id)).toEqual(["r5", "r3"]);

        const ghost = makeRun("ghost", { createdAt: "2026-06-22T12:03:30.000Z" });
        resolveStale({ runs: [ghost], total: 10 });

        // Without drift detection this page (fetched at the pre-removal
        // offset 3) would land at what is now rank 2, and the run that
        // belongs there — r2 — would never be fetched at all.
        const r2 = makeRun("r2", { createdAt: "2026-06-22T12:02:00.000Z" });
        vi.mocked(runsApi.getAll).mockResolvedValueOnce({ runs: [r2], total: 9 });
        await vi.waitFor(() => {
            expect(runsApi.getAll).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 2 }));
        });
        await vi.waitFor(() => {
            expect(src.items.map((r) => r.id)).toContain("r2");
        });

        expect(src.items.map((r) => r.id)).toEqual(["r5", "r3", "r2"]);
    });
});

// A live SSE row is merged through `upsert`, which re-evaluates the active
// filter so a row that doesn't match is never inserted. This guards the
// server↔client filter parity that paginated + virtualized + SSE-merged lists
// depend on. We drive `upsert` after an empty initial load and check whether
// the row lands.
describe("createRunsSource SSE filter parity (matchesFilters)", () => {
    async function loadedWith(overrides: Partial<RunsFilters>): Promise<RunsSource> {
        return loadedWithRuns(overrides, []);
    }

    async function loadedWithRuns(
        overrides: Partial<RunsFilters>,
        runs: Run[],
    ): Promise<RunsSource> {
        const src = createRunsSource();
        vi.mocked(runsApi.getAll).mockResolvedValue({ runs, total: runs.length });
        src.setFilters(baseFilters(overrides));
        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });
        return src;
    }

    it("matches an ended run by its end reason, not its phase", async () => {
        // Regression: the filter set holds display statuses ("failed"), but the
        // run's phase is "ended" — matching must consult displayStatus.
        const src = await loadedWith({ statuses: ["failed"] });
        src.upsert(makeRun("a", { status: "ended", endReason: "failed" }));
        src.upsert(makeRun("b", { status: "ended", endReason: "succeeded" }));
        expect(src.items.map((r) => r.id)).toEqual(["a"]);
    });

    it("matches an active run by its phase", async () => {
        // displayStatus returns the phase for any non-ended run (ignoring the
        // end reason), so the "running" filter matches on phase alone.
        const src = await loadedWith({ statuses: ["running"] });
        src.upsert(makeRun("a", { status: "running" }));
        src.upsert(makeRun("b", { status: "ended", endReason: "succeeded" }));
        expect(src.items.map((r) => r.id)).toEqual(["a"]);
    });

    it("gates on the createdAt time range, inclusive of the bounds", async () => {
        const src = await loadedWith({
            createdAfter: "2026-06-22T11:00:00.000Z",
            createdBefore: "2026-06-22T13:00:00.000Z",
        });
        src.upsert(makeRun("in", { createdAt: "2026-06-22T12:00:00.000Z" }));
        src.upsert(makeRun("early", { createdAt: "2026-06-22T10:00:00.000Z" }));
        src.upsert(makeRun("late", { createdAt: "2026-06-22T14:00:00.000Z" }));
        expect(src.items.map((r) => r.id)).toEqual(["in"]);
    });

    it("gates on triggeredBy", async () => {
        const src = await loadedWith({ triggeredBy: "cron" });
        src.upsert(makeRun("cron", { triggeredBy: "cron" }));
        src.upsert(makeRun("api", { triggeredBy: "api" }));
        expect(src.items.map((r) => r.id)).toEqual(["cron"]);
    });

    it("gates on an exact exit code", async () => {
        const src = await loadedWith({ exitCode: "137" });
        src.upsert(makeRun("oom", { status: "ended", endReason: "failed", exitCode: 137 }));
        src.upsert(makeRun("ok", { status: "ended", endReason: "succeeded", exitCode: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["oom"]);
    });

    it("gates on an exit-code range expression", async () => {
        const src = await loadedWith({ exitCode: ">100 <150" });
        src.upsert(makeRun("oom", { status: "ended", endReason: "failed", exitCode: 137 }));
        src.upsert(makeRun("high", { status: "ended", endReason: "failed", exitCode: 200 }));
        src.upsert(makeRun("ok", { status: "ended", endReason: "succeeded", exitCode: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["oom"]);
    });

    it("gates on retries-only", async () => {
        const src = await loadedWith({ retriesOnly: true });
        src.upsert(makeRun("retried", { retryAttempt: 2 }));
        src.upsert(makeRun("first", { retryAttempt: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["retried"]);
    });

    // Regression: ascending sort ("oldest first") previously bumped `total`
    // for a new SSE row without ever splicing it into `items`, undercounting
    // the visible list relative to `total` even though every earlier page was
    // already loaded.
    it("appends a new run directly when ascending-sorted and every page is already loaded", async () => {
        const src = await loadedWithRuns({ sortDirection: "asc" }, [makeRun("a"), makeRun("b")]);
        src.upsert(makeRun("c"));
        expect(src.items.map((r) => r.id)).toEqual(["a", "b", "c"]);
        expect(src.total).toBe(3);
    });

    // Guards M3: the optimistic remove and the server's run.deleted SSE echo
    // both call remove() for the same id. Only the call that actually removes a
    // row may decrement total, or the count drifts below the true value.
    it("decrements total once even when remove is called twice for one id", async () => {
        const src = createRunsSource();
        vi.mocked(runsApi.getAll).mockResolvedValue({
            runs: [makeRun("a"), makeRun("b")],
            total: 2,
        });
        src.setFilters(baseFilters());
        await vi.waitFor(() => {
            expect(src.loaded).toBe(true);
        });

        src.remove("a"); // present → removes row, total 2 → 1
        src.remove("a"); // already gone → must not touch total

        expect(src.items.map((r) => r.id)).toEqual(["b"]);
        expect(src.total).toBe(1);
    });
});
