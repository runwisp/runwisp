// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { tasksApi, AuthRequiredError } from "$lib/api";
import { toast, extractErrorMessage, runPhaseOrder } from "@runwisp/ui";
import { connectionStore } from "$lib/stores/connection.svelte";
import { sortByCreatedAtDesc } from "$lib/utils/sort";
import type { Task, Run } from "$lib/types";

export interface TaskStoreDeps {
    getTasks?: () => Promise<Task[]>;
    /** Reports a fetch failure to the connection tracker; returns true when the
     * error looks like a lost connection rather than a server-side error. */
    reportFetchError?: (err: unknown) => boolean;
    notifyError?: (message: string) => void;
}

class TaskStore {
    #items = $state<Task[]>([]);
    #loaded = $state(false);

    readonly #getTasks: () => Promise<Task[]>;
    readonly #reportFetchError: (err: unknown) => boolean;
    readonly #notifyError: (message: string) => void;

    constructor(deps: Required<TaskStoreDeps>) {
        this.#getTasks = deps.getTasks;
        this.#reportFetchError = deps.reportFetchError;
        this.#notifyError = deps.notifyError;
    }

    get items(): Task[] {
        return this.#items;
    }

    set items(value: Task[]) {
        this.#items = value;
    }

    get loaded(): boolean {
        return this.#loaded;
    }

    async loadIfNeeded(): Promise<void> {
        if (this.#loaded) return;
        try {
            const list = await this.#getTasks();
            this.#items = list;
            this.#loaded = true;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            const isConnectionErr = this.#reportFetchError(err);
            const message = isConnectionErr
                ? "Connection lost"
                : extractErrorMessage(err, "Failed to load tasks");
            this.#notifyError(message);
        }
    }
}

/** Construct a task store. Tests pass `deps` to inject fakes; the default
 * singleton uses the real tasks API, connection tracker, and toast. */
export function createTaskStore(deps: TaskStoreDeps = {}): TaskStore {
    return new TaskStore({
        getTasks: deps.getTasks ?? (() => tasksApi.getAll()),
        reportFetchError: deps.reportFetchError ?? ((err) => connectionStore.reportFetchError(err)),
        notifyError: deps.notifyError ?? ((message) => toast.error(message)),
    });
}

export const taskStore = createTaskStore();

export function upsertRun(list: Run[], run: Run): Run[] {
    const idx = list.findIndex((r) => r.id === run.id);
    if (idx !== -1) {
        const existing = list[idx];
        if (!existing) return list;
        // Never regress a run's status (e.g. stale HTTP response arriving
        // after an SSE event already advanced the status).
        if (runPhaseOrder(run.status) < runPhaseOrder(existing.status)) {
            return list;
        }
        const copy = [...list];
        copy[idx] = run;
        return copy;
    }
    return sortByCreatedAtDesc([run, ...list]);
}

export function removeRun(list: Run[], runId: string): Run[] {
    return list.filter((r) => r.id !== runId);
}
