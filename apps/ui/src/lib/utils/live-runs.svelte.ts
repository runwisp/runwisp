// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { runsApi } from "$lib/api";
import { runUpdatesStore, connectionStore } from "$lib/stores";
import { createRunsSource } from "./runs-source.svelte";
import { createLogSession } from "./log-session";
import { RunDeepLink } from "./run-deep-link.svelte";

/**
 * The live run list behind the /runs view and a task's page: a paginated
 * source kept current by run SSE events, its log callbacks, and the deep-link
 * resolver. Pass `getTaskName` to scope live events to one task. Call during
 * component init (it registers effects).
 */
export function createLiveRuns(getTaskName?: () => string) {
    const source = createRunsSource();

    const logSession = createLogSession((runId) => source.items.find((r) => r.id === runId));

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            const taskName = getTaskName?.();
            if (event.type === "run.deleted") {
                if (taskName !== undefined && event.data.taskName !== taskName) return;
                source.remove(event.data.runId);
                return;
            }
            if (taskName !== undefined && event.data.run.taskName !== taskName) return;
            source.upsert(event.data.run);
        });
    });

    // Resync after a genuine SSE reconnect (fires only on recovery from a prior
    // connection). Covers the rare gap that outlived the server's replay buffer
    // with true DB state, not a mask over live counts.
    $effect(() =>
        connectionStore.onReconnect(() => {
            source.refresh();
        }),
    );

    // Whether the deep-linked run is still loading or resolved to no run,
    // surfaced so a dead permalink shows a "not found" panel instead of quietly
    // selecting another run. The caller feeds it from an $effect.
    const deepLink = new RunDeepLink(
        (id) => runsApi.getById(id),
        (run) => {
            source.reveal(run);
        },
    );

    // True until the first fetch settles, and while a page is in flight.
    const loading = $derived(source.loading || !source.loaded);

    return {
        source,
        logSession,
        deepLink,
        get loading() {
            return loading;
        },
    };
}

export type LiveRuns = ReturnType<typeof createLiveRuns>;
