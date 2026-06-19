<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Run, RunSelector } from "@runwisp/common";
    import type { LogEvent, LogSlice, RunsListFilters } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import PageHeader from "@runwisp/ui/components/PageHeader.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { runsApi } from "$lib/api";

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
        getInstanceCount = () => 1,
    } = $props<{
        items: Run[];
        total: number;
        loading?: boolean;
        filters: RunsListFilters;
        onLoadMore: () => void;
        onOptimisticRemove: (ids: string[]) => void;
        onOptimisticRestore: (runs: Run[]) => void;
        getInstanceCount?: (taskName: string) => number;
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
    }>();

    let userSelectedRunId = $state<string | null>(null);

    const UNDO_MS = 5000;

    async function handleBulkDelete(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        const removedIds = new Set(affected.map((r) => r.id));
        const snapshot = items.filter((r: Run) => removedIds.has(r.id));
        onOptimisticRemove([...removedIds]);
        if (userSelectedRunId && removedIds.has(userSelectedRunId)) userSelectedRunId = null;

        try {
            const count = await runsApi.bulkDelete(selector);
            const restoreSelector: RunSelector = {
                match_all: false,
                ids: [...removedIds],
            };
            toast.success(count === 1 ? "Run deleted" : `${count} runs deleted`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => void undoDelete(restoreSelector, snapshot),
                },
            });
        } catch (err) {
            onOptimisticRestore(snapshot);
            toast.error(extractErrorMessage(err, "Failed to delete runs"));
        }
    }

    async function undoDelete(selector: RunSelector, snapshot: Run[]) {
        try {
            await runsApi.bulkRestore(selector);
            // Restore optimistically — SSE run.updated will also splice them
            // back in if not already.
            onOptimisticRestore(snapshot);
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to restore runs"));
        }
    }

    async function handleBulkCancel(selector: RunSelector, affected: Run[]) {
        if (affected.length === 0) return;
        try {
            const count = await runsApi.bulkCancel(selector);
            toast.success(count === 1 ? "Cancelled 1 run" : `Cancelled ${count} runs`);
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to cancel runs"));
        }
    }

    async function handleBulkRerun(selector: RunSelector, _affected: Run[]) {
        try {
            const { triggered } = await runsApi.bulkRerun(selector);
            if (triggered.length === 0) {
                toast.error("Could not re-run any of the selected tasks");
                return;
            }
            const label = triggered.length === 1 ? "task" : "tasks";
            toast.success(`Triggered ${triggered.length} ${label}`, {
                duration: UNDO_MS,
                action: {
                    label: "Undo",
                    onClick: () => void undoRerun(triggered),
                },
            });
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to re-run tasks"));
        }
    }

    async function undoRerun(triggered: { task_name: string; run_id: string }[]) {
        const ids = triggered.map((t) => t.run_id);
        try {
            await runsApi.bulkCancel({ match_all: false, ids });
        } catch {
            // best-effort: runs may already have finished
        }
        try {
            await runsApi.bulkDelete({ match_all: false, ids });
            toast.info("Re-run undone");
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to undo re-run"));
        }
    }

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && items.some((r: Run) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        return items[0]?.id ?? null;
    });

    let selectedRun = $derived(items.find((r: Run) => r.id === selectedRunId));

    function deleteSingle(runId: string) {
        const target = items.find((r: Run) => r.id === runId);
        if (!target) return;
        void handleBulkDelete({ match_all: false, ids: [runId] }, [target]);
    }
</script>

<PageContainer variant="flush" class="gap-4">
    <PageHeader
        title="Run History"
        subtitle="Comprehensive log of all task executions across this instance."
    />

    <!-- Main Content Area -->
    <div class="grid min-h-0 flex-1 grid-cols-1 gap-6 md:grid-cols-12">
        <RunsList
            {items}
            {total}
            {loading}
            bind:filters
            {onLoadMore}
            {selectedRunId}
            onselect={(id) => (userSelectedRunId = id)}
            showFilters
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

        <!-- Right Panel: Run Details -->
        <Card
            padding="none"
            class="flex flex-col md:col-span-8 lg:col-span-9"
            bodyClass="flex min-h-0 flex-1 flex-col"
        >
            <RunDetailPanel
                run={selectedRun}
                {fetchLogs}
                {streamLogs}
                showTaskName
                onDelete={deleteSingle}
                {getInstanceCount}
            />
        </Card>
    </div>
</PageContainer>
