// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { createTaskStore } from "./data.svelte";
import type { Task } from "@runwisp/common";

describe("TaskStore.loadIfNeeded", () => {
    const tasks: Task[] = [
        { name: "t1", manualTrigger: false, autostart: false, runOnStart: false },
    ];

    it("populates items and marks loaded on success", async () => {
        const store = createTaskStore({
            getTasks: () => Promise.resolve(tasks),
            fetchErrorMessage: () => null,
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
            fetchErrorMessage: () => null,
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
            fetchErrorMessage: () => "Connection lost",
            notifyError,
        });

        await store.loadIfNeeded();

        expect(store.loaded).toBe(false);
        expect(notifyError).toHaveBeenCalledWith("Connection lost");
    });

    it("flags a failed first load so the UI can leave its loading state, and clears it on refresh", async () => {
        let fail = true;
        const store = createTaskStore({
            getTasks: () => (fail ? Promise.reject(new Error("boom")) : Promise.resolve([])),
            fetchErrorMessage: (err, fallback) => (err instanceof Error ? err.message : fallback),
            notifyError: vi.fn(),
        });

        await store.loadIfNeeded();
        expect(store.loadFailed).toBe(true);

        fail = false;
        await store.refresh();
        expect(store.loadFailed).toBe(false);
        expect(store.loaded).toBe(true);
    });

    it("surfaces the extracted error message for a non-connection failure", async () => {
        const notifyError = vi.fn();
        const store = createTaskStore({
            getTasks: () => Promise.reject(new Error("boom")),
            fetchErrorMessage: (err, fallback) => (err instanceof Error ? err.message : fallback),
            notifyError,
        });

        await store.loadIfNeeded();

        expect(notifyError).toHaveBeenCalledWith("boom");
    });

    it("stays silent when the login flow owns the error", async () => {
        const notifyError = vi.fn();
        const store = createTaskStore({
            getTasks: () => Promise.reject(new Error("auth")),
            fetchErrorMessage: () => null,
            notifyError,
        });

        await store.loadIfNeeded();

        expect(store.loaded).toBe(false);
        expect(store.loadFailed).toBe(false);
        expect(notifyError).not.toHaveBeenCalled();
    });
});

describe("TaskStore.refresh", () => {
    const tasks: Task[] = [
        { name: "t1", manualTrigger: true, autostart: false, runOnStart: false },
    ];

    it("refetches even after the first load", async () => {
        const getTasks = vi.fn(() => Promise.resolve(tasks));
        const store = createTaskStore({
            getTasks,
            fetchErrorMessage: () => null,
            notifyError: () => {},
        });

        await store.loadIfNeeded();
        await store.refresh();

        expect(getTasks).toHaveBeenCalledTimes(2);
        expect(store.items).toEqual(tasks);
    });

    it("refreshSoon batches a burst into one refetch", async () => {
        vi.useFakeTimers();
        const getTasks = vi.fn(() => Promise.resolve(tasks));
        const store = createTaskStore({
            getTasks,
            fetchErrorMessage: () => null,
            notifyError: () => {},
        });

        store.refreshSoon();
        store.refreshSoon();
        await vi.runAllTimersAsync();
        store.refreshSoon();
        await vi.runAllTimersAsync();
        vi.useRealTimers();

        expect(getTasks).toHaveBeenCalledTimes(2);
    });

    it("keeps the current list and stays quiet on failure", async () => {
        const notifyError = vi.fn();
        const fetchErrorMessage = vi.fn(() => "Connection lost");
        const getTasks = vi
            .fn<() => Promise<Task[]>>()
            .mockResolvedValueOnce(tasks)
            .mockRejectedValueOnce(new Error("network down"));
        const store = createTaskStore({ getTasks, fetchErrorMessage, notifyError });

        await store.loadIfNeeded();
        await store.refresh();

        expect(store.items).toEqual(tasks);
        expect(fetchErrorMessage).toHaveBeenCalledTimes(1);
        expect(notifyError).not.toHaveBeenCalled();
    });
});
