<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { RunsPage } from "$lib/components/dashboard";
    import { instanceCountResolver } from "$lib/components/dashboard/instance-count";
    import { Skeleton } from "@runwisp/ui";
    import { runUpdatesStore, taskStore } from "$lib/stores";
    import { createRunsSource } from "$lib/utils/runs-source.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import type { RunsListFilters } from "@runwisp/ui";

    const source = createRunsSource();

    let getInstanceCount = $derived(instanceCountResolver(taskStore.items));

    let filters = $state<RunsListFilters>({
        search: "",
        status: "all",
        sort_direction: "desc",
    });

    $effect(() => {
        source.setFilters({ ...filters });
    });

    const logSession = createLogSession({
        findRun: (runId) => source.items.find((r) => r.id === runId),
        getTaskName: (run) => run.task_name,
    });

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                source.remove(event.data.run_id);
                return;
            }
            source.upsert(event.data.run);
        });
    });
</script>

{#if source.items.length === 0 && source.loading}
    <Skeleton rows={5} />
{:else}
    <RunsPage
        items={source.items}
        total={source.total}
        loading={source.loading}
        onLoadMore={() => source.loadMore()}
        bind:filters
        onOptimisticRemove={(ids) => ids.forEach((id) => source.remove(id))}
        onOptimisticRestore={(runs) => runs.forEach((run) => source.upsert(run))}
        fetchLogs={logSession.fetchLogs}
        streamLogs={logSession.streamLogs}
        fetchLineHistory={logSession.fetchLineHistory}
        {getInstanceCount}
    />
{/if}
