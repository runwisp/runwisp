// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { tasksApi, AuthRequiredError } from "$lib/api";
import { toast, extractErrorMessage, runPhaseOrder } from "@runwisp/ui";
import { connectionStore } from "$lib/stores/connection.svelte";
import { sortByCreatedAtDesc } from "$lib/utils/sort";
import type { Task, Run } from "$lib/types";

class TaskStore {
    #items = $state<Task[]>([]);
    #loaded = $state(false);

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
            const list = await tasksApi.getAll();
            this.#items = list;
            this.#loaded = true;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            const isConnectionErr = connectionStore.reportFetchError(err);
            const message = isConnectionErr
                ? "Connection lost"
                : extractErrorMessage(err, "Failed to load tasks");
            toast.error(message);
        }
    }
}

export const taskStore = new TaskStore();

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
