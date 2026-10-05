<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { RunsPage } from "$lib/components/dashboard";
    import { instanceCountResolver } from "$lib/components/dashboard/instance-count";
    import { runsApi } from "$lib/api";
    import { runUpdatesStore, taskStore, connectionStore } from "$lib/stores";
    import { createRunsSource } from "$lib/utils/runs-source.svelte";
    import { RunDeepLink } from "$lib/utils/run-deep-link.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { navigateToRun } from "$lib/utils/run-url";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    const source = createRunsSource();

    // The selected run lives in the path as an optional segment: /runs/{runId}.
    let runIdParam = $derived($page.params.runId ?? null);

    // Mirror the user-selected run into the address bar so the URL is shareable;
    // null drops back to /runs.
    function selectRun(runId: string | null) {
        navigateToRun($page.url, runId ? resolve(`/runs/${runId}`) : resolve("/runs"));
    }

    let getInstanceCount = $derived(instanceCountResolver(taskStore.items));

    let filters = $state<RunsListFilters>(emptyRunFilters());

    $effect(() => {
        source.setFilters({ ...filters });
    });

    const logSession = createLogSession({
        findRun: (runId) => source.items.find((r) => r.id === runId),
        getTaskName: (run) => run.taskName,
    });

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                source.remove(event.data.runId);
                return;
            }
            source.upsert(event.data.run);
        });
    });

    // Resync after a genuine SSE reconnect (fires only on recovery), covering the
    // rare gap that outlived the server's replay buffer with true DB state.
    $effect(() => connectionStore.onReconnect(() => source.refresh()));

    // Whether the deep-linked run is still loading or resolved to no run,
    // surfaced so a dead permalink shows a "not found" panel instead of quietly
    // selecting another run.
    const deepLink = new RunDeepLink(
        (id) => runsApi.getById(id),
        (run) => source.upsert(run),
    );
    $effect(() => deepLink.resolve(runIdParam, source.items));
</script>

<RunsPage
    items={source.items}
    total={source.total}
    loading={source.loading || !source.loaded}
    onLoadMore={() => source.loadMore()}
    bind:filters
    onOptimisticRemove={(ids) => ids.forEach((id) => source.remove(id))}
    onOptimisticRestore={(runs) => runs.forEach((run) => source.upsert(run))}
    fetchLogs={logSession.fetchLogs}
    streamLogs={logSession.streamLogs}
    fetchLineHistory={logSession.fetchLineHistory}
    motion={source.motion}
    {getInstanceCount}
    initialRunId={runIdParam}
    runNotFound={deepLink.notFound}
    runPending={deepLink.pending}
    onSelectRun={selectRun}
/>
