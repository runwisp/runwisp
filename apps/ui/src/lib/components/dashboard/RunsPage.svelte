<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Run, RunSelector } from "@runwisp/common";
    import type { LogEvent, LogSlice } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import { RunsList, RunDetailPanel, toast, extractErrorMessage } from "@runwisp/ui";
    import { sortByCreatedAtDesc } from "$lib/utils/sort";
    import { runsApi } from "$lib/api";

    let {
        runs = $bindable([]),
        fetchLogs,
        streamLogs,
    } = $props<{
        runs: Run[];
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
        const snapshot = runs.filter((r: Run) => removedIds.has(r.id));
        runs = runs.filter((r: Run) => !removedIds.has(r.id));
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
            runs = sortByCreatedAtDesc([...runs, ...snapshot]);
            toast.error(extractErrorMessage(err, "Failed to delete runs"));
        }
    }

    async function undoDelete(selector: RunSelector, snapshot: Run[]) {
        try {
            await runsApi.bulkRestore(selector);
            // Restore optimistically — SSE run.updated will also splice them
            // back in if not already.
            runs = sortByCreatedAtDesc([
                ...runs.filter((r: Run) => !snapshot.some((s) => s.id === r.id)),
                ...snapshot,
            ]);
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

    let sortedRuns: Run[] = $derived(sortByCreatedAtDesc(runs));

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && sortedRuns.some((r) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        return sortedRuns[0]?.id ?? null;
    });

    let selectedRun = $derived(runs.find((r: Run) => r.id === selectedRunId));

    function deleteSingle(runId: string) {
        const target = runs.find((r: Run) => r.id === runId);
        if (!target) return;
        void handleBulkDelete({ match_all: false, ids: [runId] }, [target]);
    }
</script>

<PageContainer variant="flush" class="gap-4">
    <!-- Header -->
    <div class="flex shrink-0 items-center justify-between px-1">
        <div>
            <h1 class="text-2xl font-bold tracking-tight text-slate-900">Run History</h1>
            <p class="mt-0.5 text-sm text-slate-500">
                Comprehensive log of all task executions across this instance.
            </p>
        </div>
    </div>

    <!-- Main Content Area -->
    <div class="grid min-h-0 flex-1 grid-cols-1 gap-6 md:grid-cols-12">
        <RunsList
            {runs}
            {selectedRunId}
            onselect={(id) => (userSelectedRunId = id)}
            showFilters
            showTaskName
            headerLabel="Runs"
            emptyText="No runs found"
            bulkActions
            onBulkCancel={handleBulkCancel}
            onBulkDelete={handleBulkDelete}
            onBulkRerun={handleBulkRerun}
        />

        <!-- Right Panel: Run Details -->
        <div
            class="flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm md:col-span-8 lg:col-span-9"
        >
            <RunDetailPanel
                run={selectedRun}
                {fetchLogs}
                {streamLogs}
                showTaskName
                onDelete={deleteSingle}
            />
        </div>
    </div>
</PageContainer>
