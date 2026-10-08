<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { RunsPage } from "$lib/components/dashboard";
    import { instanceCountResolver } from "$lib/utils/task";
    import { taskStore } from "$lib/stores";
    import { createLiveRuns } from "$lib/utils/live-runs.svelte";
    import { navigateToRun } from "$lib/utils/run-url";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    const { source, logSession, deepLink } = createLiveRuns();

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
