// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
        task_name: "backup-db",
        created_at: "2026-06-22T12:00:00.000Z",
        status: "ended",
        end_reason: "success",
        triggered_by: "api",
        exit_code: 0,
        instance_index: 0,
        retry_attempt: 0,
        ...overrides,
    };
}

const baseFilters = (overrides: Partial<RunsFilters> = {}): RunsFilters => ({
    search: "",
    statuses: [],
    sort_direction: "desc",
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

    it("fetches a task's own runs through tasksApi when task_name is set", async () => {
        const src = createRunsSource();
        vi.mocked(tasksApi.getRuns).mockResolvedValue({ runs: [], total: 0 });

        src.setFilters(baseFilters({ task_name: "backup-db" }));

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
});

// A live SSE row is merged through `upsert`, which re-evaluates the active
// filter so a row that doesn't match is never inserted. This guards the
// server↔client filter parity that paginated + virtualized + SSE-merged lists
// depend on. We drive `upsert` after an empty initial load and check whether
// the row lands.
describe("createRunsSource SSE filter parity (matchesFilters)", () => {
    async function loadedWith(overrides: Partial<RunsFilters>): Promise<RunsSource> {
        const src = createRunsSource();
        vi.mocked(runsApi.getAll).mockResolvedValue({ runs: [], total: 0 });
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
        src.upsert(makeRun("a", { status: "ended", end_reason: "failed" }));
        src.upsert(makeRun("b", { status: "ended", end_reason: "success" }));
        expect(src.items.map((r) => r.id)).toEqual(["a"]);
    });

    it("matches an active run by its phase", async () => {
        // displayStatus returns the phase for any non-ended run (ignoring the
        // end reason), so the "running" filter matches on phase alone.
        const src = await loadedWith({ statuses: ["running"] });
        src.upsert(makeRun("a", { status: "running" }));
        src.upsert(makeRun("b", { status: "ended", end_reason: "success" }));
        expect(src.items.map((r) => r.id)).toEqual(["a"]);
    });

    it("gates on the created_at time range, inclusive of the bounds", async () => {
        const src = await loadedWith({
            created_after: "2026-06-22T11:00:00.000Z",
            created_before: "2026-06-22T13:00:00.000Z",
        });
        src.upsert(makeRun("in", { created_at: "2026-06-22T12:00:00.000Z" }));
        src.upsert(makeRun("early", { created_at: "2026-06-22T10:00:00.000Z" }));
        src.upsert(makeRun("late", { created_at: "2026-06-22T14:00:00.000Z" }));
        expect(src.items.map((r) => r.id)).toEqual(["in"]);
    });

    it("gates on triggered_by", async () => {
        const src = await loadedWith({ triggered_by: "cron" });
        src.upsert(makeRun("cron", { triggered_by: "cron" }));
        src.upsert(makeRun("api", { triggered_by: "api" }));
        expect(src.items.map((r) => r.id)).toEqual(["cron"]);
    });

    it("gates on an exact exit code", async () => {
        const src = await loadedWith({ exit_code: "137" });
        src.upsert(makeRun("oom", { status: "ended", end_reason: "failed", exit_code: 137 }));
        src.upsert(makeRun("ok", { status: "ended", end_reason: "success", exit_code: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["oom"]);
    });

    it("gates on an exit-code range expression", async () => {
        const src = await loadedWith({ exit_code: ">100 <150" });
        src.upsert(makeRun("oom", { status: "ended", end_reason: "failed", exit_code: 137 }));
        src.upsert(makeRun("high", { status: "ended", end_reason: "failed", exit_code: 200 }));
        src.upsert(makeRun("ok", { status: "ended", end_reason: "success", exit_code: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["oom"]);
    });

    it("gates on retries-only", async () => {
        const src = await loadedWith({ retries_only: true });
        src.upsert(makeRun("retried", { retry_attempt: 2 }));
        src.upsert(makeRun("first", { retry_attempt: 0 }));
        expect(src.items.map((r) => r.id)).toEqual(["retried"]);
    });
});
