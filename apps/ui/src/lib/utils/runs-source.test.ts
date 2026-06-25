// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Run } from "@runwisp/common";

vi.mock("$lib/api", () => ({
    runsApi: { getAll: vi.fn() },
    tasksApi: { getRuns: vi.fn() },
}));

import { runsApi, tasksApi } from "$lib/api";
import { createRunsSource, type RunsFilters } from "./runs-source.svelte";

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
    status: "all",
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
