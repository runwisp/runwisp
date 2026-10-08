<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { untrack } from "svelte";
    import type { RunsListFilters } from "@runwisp/ui";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { headerSearchStore, systemStore, taskStore } from "$lib/stores";
    import type { LiveRuns } from "$lib/utils/live-runs.svelte";
    import { createRunSelection } from "$lib/utils/run-selection.svelte";
    import { HistoryRail } from "$lib/utils/history-rail.svelte";
    import { instanceCountResolver } from "$lib/utils/task";

    let {
        live,
        filters = $bindable(),
        initialRunId,
        onSelectRun,
    }: {
        live: LiveRuns;
        filters: RunsListFilters;
        initialRunId: string | null;
        // Reports explicit picks (not the auto-fallback) so the URL can mirror them.
        onSelectRun: (runId: string | null) => void;
    } = $props();

    const getInstanceCount = $derived(instanceCountResolver(taskStore.items));

    const rail = new HistoryRail(untrack(() => !!initialRunId));
    // `live` and `onSelectRun` are fixed for the page's lifetime.
    // svelte-ignore state_referenced_locally
    const selection = createRunSelection({
        live,
        getInitialRunId: () => initialRunId,
        onSeeded: () => rail.picked(),
        onSelectRun,
    });

    // The header search filters this list by task name or run ID.
    $effect(() => {
        headerSearchStore.register({
            placeholder: "Search runs by task or ID…",
            onSearch: (q) => {
                filters.search = q;
                rail.searched(q);
            },
        });
        return () => headerSearchStore.unregister();
    });

    let panes = $derived(rail.panes(!!selection.selectedRun, false));
</script>

<!-- Card-less, full-bleed: the history rail and detail panel fill the content
     area edge-to-edge (cancelling AppLayout's p-6), divided only by the rail's
     right border, the same chrome-less frame as a task's detail page. -->
<div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col md:flex-row">
    {#if panes.list}
        <RunsList
            items={live.source.items}
            total={live.source.total}
            loading={live.loading}
            bind:filters
            onLoadMore={() => live.source.loadMore()}
            selectedRunId={selection.selectedRunId}
            onselect={(id) => {
                selection.userSelectedRunId = id;
                rail.picked();
            }}
            showFilters
            showTask
            tasks={taskStore.items}
            showTaskName
            headerLabel="Runs"
            emptyText="No runs found"
            emptyDescription="Trigger a task manually with Re-run, or wait for a schedule to fire."
            bulkActions
            onBulkCancel={selection.handleBulkCancel}
            onBulkDelete={selection.handleBulkDelete}
            onBulkRerun={selection.handleBulkRerun}
            {getInstanceCount}
            motion={live.source.motion}
        />
    {/if}

    {#if panes.detail}
        <RunDetailPanel
            run={selection.selectedRun}
            {...live.logSession}
            showTaskName
            onDelete={selection.deleteSingle}
            onBack={rail.phone ? rail.back : undefined}
            onToggleList={rail.collapsible ? rail.toggleList : undefined}
            listVisible={panes.list}
            {getInstanceCount}
            getLiveUsage={(id) => systemStore.runUsage(id)}
            motion={live.source.motion}
            notFound={selection.deepLinkMissing}
            loading={(live.loading && live.source.items.length === 0) || selection.deepLinkPending}
        />
    {/if}
</div>
