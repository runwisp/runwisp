// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi } from "vitest";
import { AuthRequiredError } from "$lib/api";
import { createTaskStore, upsertRun, removeRun } from "./data.svelte";
import type { Run, Task } from "$lib/types";

function makeRun(id: string, overrides: Partial<Run> = {}): Run {
    return {
        id,
        task_name: "backup-db",
        created_at: "2026-05-05T12:00:00.000Z",
        status: "running",
        triggered_by: "cron",
        exit_code: 0,
        instance_index: 0,
        retry_attempt: 0,
        ...overrides,
    };
}

describe("upsertRun", () => {
    it("prepends a new run and keeps the list sorted by created_at desc", () => {
        const older = makeRun("a", { created_at: "2026-05-05T11:00:00.000Z" });
        const incoming = makeRun("b", { created_at: "2026-05-05T13:00:00.000Z" });

        const result = upsertRun([older], incoming);

        expect(result.map((r) => r.id)).toEqual(["b", "a"]);
    });

    it("updates an existing run in place when its status advances", () => {
        const existing = makeRun("a", { status: "running" });
        const advanced = makeRun("a", { status: "ended", end_reason: "success" });

        const result = upsertRun([existing], advanced);

        expect(result).toHaveLength(1);
        expect(result[0]?.status).toBe("ended");
        expect(result[0]?.end_reason).toBe("success");
    });

    it("rejects a status regression (stale update arriving after a later phase)", () => {
        const existing = makeRun("a", { status: "running" });
        const stale = makeRun("a", { status: "pending" });

        const result = upsertRun([existing], stale);

        // The list is returned untouched: the running row must not regress to pending.
        expect(result[0]?.status).toBe("running");
    });
});

describe("removeRun", () => {
    it("drops the run whose id matches", () => {
        const list = [makeRun("a"), makeRun("b")];
        expect(removeRun(list, "a").map((r) => r.id)).toEqual(["b"]);
    });

    it("returns the list unchanged when the id is absent", () => {
        const list = [makeRun("a")];
        expect(removeRun(list, "missing")).toHaveLength(1);
    });
});

describe("TaskStore.loadIfNeeded", () => {
    const tasks: Task[] = [{ name: "t1", api_trigger: false, autostart: false }];

    it("populates items and marks loaded on success", async () => {
        const store = createTaskStore({
            getTasks: () => Promise.resolve(tasks),
            reportFetchError: () => false,
            notifyError: () => {},
        });

        await store.loadIfNeeded();

        expect(store.loaded).toBe(true);
        expect(store.items).toEqual(tasks);
    });

    it("only fetches once across repeated calls", async () => {
        const getTasks = vi.fn(() => Promise.resolve(tasks));
        const store = createTaskStore({
            getTasks,
            reportFetchError: () => false,
            notifyError: () => {},
        });

        await store.loadIfNeeded();
        await store.loadIfNeeded();

        expect(getTasks).toHaveBeenCalledTimes(1);
    });

    it("reports 'Connection lost' when the error is a connection failure", async () => {
        const notifyError = vi.fn();
        const store = createTaskStore({
            getTasks: () => Promise.reject(new Error("network down")),
            reportFetchError: () => true,
            notifyError,
        });

        await store.loadIfNeeded();

        expect(store.loaded).toBe(false);
        expect(notifyError).toHaveBeenCalledWith("Connection lost");
    });

    it("surfaces the extracted error message for a non-connection failure", async () => {
        const notifyError = vi.fn();
        const store = createTaskStore({
            getTasks: () => Promise.reject(new Error("boom")),
            reportFetchError: () => false,
            notifyError,
        });

        await store.loadIfNeeded();

        expect(notifyError).toHaveBeenCalledWith("boom");
    });

    it("stays silent on AuthRequiredError (the login flow handles it)", async () => {
        const notifyError = vi.fn();
        const reportFetchError = vi.fn(() => false);
        const store = createTaskStore({
            getTasks: () => Promise.reject(new AuthRequiredError()),
            reportFetchError,
            notifyError,
        });

        await store.loadIfNeeded();

        expect(store.loaded).toBe(false);
        expect(reportFetchError).not.toHaveBeenCalled();
        expect(notifyError).not.toHaveBeenCalled();
    });
});
