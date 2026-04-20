// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { tasksApi, AuthRequiredError } from "$lib/api";
import { toast, extractErrorMessage } from "@runwisp/ui";
import { sortByCreatedAtDesc } from "$lib/utils/sort";
import type { Task, Run } from "$lib/types";

function createTaskStore() {
    let items = $state<Task[]>([]);
    let loaded = $state(false);

    async function loadIfNeeded() {
        if (loaded) return;
        try {
            const list = await tasksApi.getAll();
            items = list;
            loaded = true;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            toast.error(extractErrorMessage(err, "Failed to load tasks"));
        }
    }

    return {
        get items() {
            return items;
        },
        set items(v: Task[]) {
            items = v;
        },
        get loaded() {
            return loaded;
        },
        loadIfNeeded,
    };
}

export const taskStore = createTaskStore();

/** Status progression index — higher means further along in lifecycle. */
const PHASE_ORDER: Record<string, number> = { pending: 0, running: 1, ended: 2 };

export function upsertRun(list: Run[], run: Run): Run[] {
    const idx = list.findIndex((r) => r.id === run.id);
    if (idx !== -1) {
        const existing = list[idx];
        if (!existing) return list;
        // Never regress a run's status (e.g. stale HTTP response arriving
        // after an SSE event already advanced the status).
        if ((PHASE_ORDER[run.status] ?? 0) < (PHASE_ORDER[existing.status] ?? 0)) {
            return list;
        }
        const copy = [...list];
        copy[idx] = run;
        return copy;
    }
    return sortByCreatedAtDesc([run, ...list]);
}
