// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { tasksApi } from "$lib/api";
import { toast } from "@runwisp/ui";
import { connectionStore } from "$lib/stores/connection.svelte";
import type { Task } from "@runwisp/common";

/** How long refreshSoon waits, so a burst of run events costs one refetch. */
const REFRESH_SOON_MS = 1000;

interface TaskStoreDeps {
    getTasks?: () => Promise<Task[]>;
    /** Reports a fetch failure to the connection tracker and returns the message
     * to show, or null when the login flow owns it. */
    fetchErrorMessage?: (err: unknown, fallback: string) => string | null;
    notifyError?: (message: string) => void;
}

class TaskStore {
    #items = $state<Task[]>([]);
    #loaded = $state(false);
    #loadFailed = $state(false);
    #refreshTimer: ReturnType<typeof setTimeout> | null = null;

    readonly #getTasks: () => Promise<Task[]>;
    readonly #fetchErrorMessage: (err: unknown, fallback: string) => string | null;
    readonly #notifyError: (message: string) => void;

    constructor(deps: Required<TaskStoreDeps>) {
        this.#getTasks = deps.getTasks;
        this.#fetchErrorMessage = deps.fetchErrorMessage;
        this.#notifyError = deps.notifyError;
    }

    get items(): Task[] {
        return this.#items;
    }

    get loaded(): boolean {
        return this.#loaded;
    }

    /** True when the first load failed and nothing has succeeded since, so the
     * UI can stop showing a loading state. A later refresh() clears it. */
    get loadFailed(): boolean {
        return this.#loadFailed;
    }

    /** Fetch the task list into the store. Rejects on failure, leaving the
     * error to the caller (pages pass it to AsyncData for their error UI). */
    async load(): Promise<Task[]> {
        const items = await this.#getTasks();
        this.#items = items;
        this.#loaded = true;
        this.#loadFailed = false;
        return items;
    }

    async loadIfNeeded(): Promise<void> {
        if (!this.#loaded) await this.refresh({ notify: true });
    }

    /** Refetch the task list. Keeps the current list on failure; only the first
     * load (`notify`) toasts, since a later connection loss is already surfaced
     * by the connection tracker. */
    async refresh({ notify = false }: { notify?: boolean } = {}): Promise<void> {
        try {
            await this.load();
        } catch (err) {
            const message = this.#fetchErrorMessage(err, "Failed to load tasks");
            if (message === null) return;
            if (!this.#loaded) this.#loadFailed = true;
            if (notify) this.#notifyError(message);
        }
    }

    /** Refetch shortly. Calls inside the window ride on the first one, so a
     * burst of simultaneous cron fires costs a single refetch. */
    refreshSoon(): void {
        if (this.#refreshTimer) return;
        this.#refreshTimer = setTimeout(() => {
            this.#refreshTimer = null;
            void this.refresh();
        }, REFRESH_SOON_MS);
    }
}

/** Construct a task store. Tests pass `deps` to inject fakes; the default
 * singleton uses the real tasks API, connection tracker, and toast. */
export function createTaskStore(deps: TaskStoreDeps = {}): TaskStore {
    return new TaskStore({
        getTasks: deps.getTasks ?? (() => tasksApi.getAll()),
        fetchErrorMessage:
            deps.fetchErrorMessage ??
            ((err, fallback) => connectionStore.fetchErrorMessage(err, fallback)),
        notifyError: deps.notifyError ?? ((message) => toast.error(message)),
    });
}

export const taskStore = createTaskStore();
