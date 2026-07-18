<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Run } from "@runwisp/common";
    import type { LogEvent, LogSlice, RunsListFilters } from "@runwisp/ui";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { headerSearchStore, taskStore } from "$lib/stores";
    import { createRunActions } from "$lib/utils/run-actions";

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
        initialRunId = null,
        runNotFound = false,
        onSelectRun,
    } = $props<{
        items: Run[];
        total: number;
        loading?: boolean;
        filters: RunsListFilters;
        onLoadMore: () => void;
        onOptimisticRemove: (ids: string[]) => void;
        onOptimisticRestore: (runs: Run[]) => void;
        getInstanceCount?: (taskName: string) => number;
        initialRunId?: string | null;
        // True when the deep-linked run id (initialRunId) was fetched and doesn't
        // exist. Distinguishes "deleted/bad permalink" from a stale selection that
        // merely scrolled out of the loaded window.
        runNotFound?: boolean;
        // Notified when the user picks a run, so the route can mirror it into
        // the address bar. The auto-fallback to newest is not reported.
        onSelectRun?: (runId: string | null) => void;
        fetchLogs: (
            runId: string,
            from: number,
            to: number,
        ) => Promise<LogSlice | LogEvent | void> | LogSlice | LogEvent | void;
        streamLogs?: (
            runId: string,
            onEvent: (event: LogEvent) => void,
            initialState?: { fromLine: number },
        ) => () => void;
        fetchLineHistory?: (runId: string, lineNum: number) => Promise<string[][]>;
    }>();

    let userSelectedRunId = $state<string | null>(null);

    // Seed the selection from a deep link (the run-id path segment), on load and
    // on later URL changes. Declared before the emit effect below so the first
    // flush seeds before it reports — otherwise the initial null would clobber it.
    $effect(() => {
        if (initialRunId) userSelectedRunId = initialRunId;
    });

    // Report explicit selections upward so the URL can mirror the run on screen.
    $effect(() => {
        onSelectRun?.(userSelectedRunId);
    });

    // The header search filters this list by task name or run ID.
    $effect(() => {
        headerSearchStore.register({
            placeholder: "Search runs by task or ID…",
            onSearch: (q) => (filters.search = q),
        });
        return () => headerSearchStore.unregister();
    });

    const { handleBulkDelete, handleBulkCancel, handleBulkRerun, deleteSingle } = createRunActions({
        getItems: () => items,
        onOptimisticRemove: (ids) => onOptimisticRemove(ids),
        onOptimisticRestore: (runs) => onOptimisticRestore(runs),
        onRemoved: (ids) => {
            if (userSelectedRunId && ids.has(userSelectedRunId)) userSelectedRunId = null;
        },
    });

    // The deep-linked run genuinely doesn't exist: its id is the current URL
    // selection, the fetch confirmed it missing, and it isn't in the list. In
    // that case we show a "not found" panel instead of silently falling back to
    // the newest run under a URL that still points at the dead id.
    let deepLinkMissing = $derived(
        runNotFound &&
            userSelectedRunId !== null &&
            userSelectedRunId === initialRunId &&
            !items.some((r: Run) => r.id === userSelectedRunId),
    );

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && items.some((r: Run) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        if (deepLinkMissing) return null;
        return items[0]?.id ?? null;
    });

    let selectedRun = $derived(items.find((r: Run) => r.id === selectedRunId));
</script>

<!-- Card-less, full-bleed: the history rail and detail panel fill the content
     area edge-to-edge (cancelling AppLayout's p-6), divided only by the rail's
     right border — the same chrome-less frame as a task's detail page. -->
<div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col md:flex-row">
    <RunsList
        flush
        {items}
        {total}
        {loading}
        bind:filters
        {onLoadMore}
        {selectedRunId}
        onselect={(id) => (userSelectedRunId = id)}
        showFilters
        showTask
        tasks={taskStore.items}
        showTaskName
        headerLabel="Runs"
        emptyText="No runs found"
        emptyDescription="Trigger a task manually with Re-run, or wait for a schedule to fire."
        bulkActions
        onBulkCancel={handleBulkCancel}
        onBulkDelete={handleBulkDelete}
        onBulkRerun={handleBulkRerun}
        {getInstanceCount}
    />

    <RunDetailPanel
        run={selectedRun}
        {fetchLogs}
        {streamLogs}
        {fetchLineHistory}
        showTaskName
        onDelete={deleteSingle}
        {getInstanceCount}
        notFound={deepLinkMissing}
    />
</div>
