<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { untrack } from "svelte";
    import type { Run } from "@runwisp/common";
    import type { LogEvent, RunMotion, RunsListFilters } from "@runwisp/ui";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { headerSearchStore, systemStore, taskStore } from "$lib/stores";
    import { createRunSelection } from "$lib/utils/run-selection.svelte";
    import { HistoryRail } from "$lib/utils/history-rail.svelte";

    let {
        items,
        total,
        loading = false,
        filters = $bindable(),
        onLoadMore,
        onOptimisticRemove,
        onOptimisticRestore,
        fetchLogs,
        streamLogs,
        fetchLineHistory,
        getInstanceCount = () => 1,
        motion,
        initialRunId = null,
        runNotFound = false,
        runPending = false,
        onSelectRun,
    }: {
        items: Run[];
        total: number;
        loading?: boolean;
        filters: RunsListFilters;
        onLoadMore: () => void;
        onOptimisticRemove: (ids: string[]) => void;
        onOptimisticRestore: (runs: Run[]) => void;
        getInstanceCount?: (taskName: string) => number;
        // Runs that arrived or were removed live moments ago; they animate.
        motion: RunMotion;
        initialRunId?: string | null;
        // True when the deep-linked run id (initialRunId) was fetched and doesn't
        // exist. Distinguishes "deleted/bad permalink" from a stale selection that
        // merely scrolled out of the loaded window.
        runNotFound?: boolean;
        // True while the deep-linked run (initialRunId) is being fetched because
        // it isn't in the loaded list yet. Holds the detail panel on a loading
        // state rather than flashing another run first.
        runPending?: boolean;
        // Notified when the user picks a run, so the route can mirror it into
        // the address bar. The auto-fallback to newest is not reported.
        onSelectRun?: (runId: string | null) => void;
        fetchLogs: (runId: string, from: number, to: number) => Promise<LogEvent>;
        streamLogs: (
            runId: string,
            onEvent: (event: LogEvent) => void,
            initialState?: { fromLine: number },
        ) => () => void;
        fetchLineHistory: (runId: string, lineNum: number) => Promise<string[][]>;
    } = $props();

    const rail = new HistoryRail(untrack(() => !!initialRunId));
    const selection = createRunSelection({
        getItems: () => items,
        getInitialRunId: () => initialRunId,
        getRunNotFound: () => runNotFound,
        getRunPending: () => runPending,
        onOptimisticRemove: (ids) => onOptimisticRemove(ids),
        onOptimisticRestore: (runs) => onOptimisticRestore(runs),
        onSeeded: () => rail.picked(),
        onSelectRun: (id) => onSelectRun?.(id),
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
            {items}
            {total}
            {loading}
            bind:filters
            {onLoadMore}
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
            {motion}
        />
    {/if}

    {#if panes.detail}
        <RunDetailPanel
            run={selection.selectedRun}
            {fetchLogs}
            {streamLogs}
            {fetchLineHistory}
            showTaskName
            onDelete={selection.deleteSingle}
            onBack={rail.phone ? rail.back : undefined}
            onToggleList={rail.collapsible ? rail.toggleList : undefined}
            listVisible={panes.list}
            {getInstanceCount}
            getLiveUsage={(id) => systemStore.runUsage(id)}
            {motion}
            notFound={selection.deepLinkMissing}
            loading={(loading && items.length === 0) || selection.deepLinkPending}
        />
    {/if}
</div>
