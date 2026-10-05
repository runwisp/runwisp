// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { tasksApi, AuthRequiredError } from "$lib/api";
import { toast, extractErrorMessage } from "@runwisp/ui";
import { connectionStore } from "$lib/stores/connection.svelte";
import type { Task, Run } from "$lib/types";

interface TaskStoreDeps {
    getTasks?: () => Promise<Task[]>;
    /** Reports a fetch failure to the connection tracker; returns true when the
     * error looks like a lost connection rather than a server-side error. */
    reportFetchError?: (err: unknown) => boolean;
    notifyError?: (message: string) => void;
}

class TaskStore {
    #items = $state<Task[]>([]);
    #loaded = $state(false);
    #loadFailed = $state(false);

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

    /** True when the first load failed and nothing has succeeded since, so the
     * UI can stop showing a loading state. A later refresh() clears it. */
    get loadFailed(): boolean {
        return this.#loadFailed;
    }

    async loadIfNeeded(): Promise<void> {
        if (this.#loaded) return;
        try {
            const list = await this.#getTasks();
            this.#items = list;
            this.#loaded = true;
            this.#loadFailed = false;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            this.#loadFailed = true;
            const isConnectionErr = this.#reportFetchError(err);
            const message = isConnectionErr
                ? "Connection lost"
                : extractErrorMessage(err, "Failed to load tasks");
            this.#notifyError(message);
        }
    }

    /** Refetch after the daemon's task set changed (reload, schedule pause).
     * Keeps the current list on failure: connection loss is already surfaced
     * by the connection tracker, and a toast here would repeat it. */
    async refresh(): Promise<void> {
        try {
            this.#items = await this.#getTasks();
            this.#loaded = true;
            this.#loadFailed = false;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            this.#reportFetchError(err);
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

export function removeRun(list: Run[], runId: string): Run[] {
    return list.filter((r) => r.id !== runId);
}
