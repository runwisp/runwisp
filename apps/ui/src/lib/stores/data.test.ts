// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { AuthRequiredError } from "$lib/api";
import { createTaskStore } from "./data.svelte";
import type { Task } from "@runwisp/common";

describe("TaskStore.loadIfNeeded", () => {
    const tasks: Task[] = [
        { name: "t1", manualTrigger: false, autostart: false, runOnStart: false },
    ];

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

    it("flags a failed first load so the UI can leave its loading state, and clears it on refresh", async () => {
        let fail = true;
        const store = createTaskStore({
            getTasks: () => (fail ? Promise.reject(new Error("boom")) : Promise.resolve([])),
            reportFetchError: () => false,
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

describe("TaskStore.refresh", () => {
    const tasks: Task[] = [
        { name: "t1", manualTrigger: true, autostart: false, runOnStart: false },
    ];

    it("refetches even after the first load", async () => {
        const getTasks = vi.fn(() => Promise.resolve(tasks));
        const store = createTaskStore({
            getTasks,
            reportFetchError: () => false,
            notifyError: () => {},
        });

        await store.loadIfNeeded();
        await store.refresh();

        expect(getTasks).toHaveBeenCalledTimes(2);
        expect(store.items).toEqual(tasks);
    });

    it("keeps the current list and stays quiet on failure", async () => {
        const notifyError = vi.fn();
        const reportFetchError = vi.fn(() => true);
        const getTasks = vi
            .fn<() => Promise<Task[]>>()
            .mockResolvedValueOnce(tasks)
            .mockRejectedValueOnce(new Error("network down"));
        const store = createTaskStore({ getTasks, reportFetchError, notifyError });

        await store.loadIfNeeded();
        await store.refresh();

        expect(store.items).toEqual(tasks);
        expect(reportFetchError).toHaveBeenCalledTimes(1);
        expect(notifyError).not.toHaveBeenCalled();
    });
});
