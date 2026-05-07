<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import type { Run } from "@runwisp/common";
    import type { LogEvent, LogSlice } from "@runwisp/ui";
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import { RunsList, RunDetailPanel } from "@runwisp/ui";
    import { sortByCreatedAtDesc } from "$lib/utils/sort";

    let {
        runs = [],
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

    let sortedRuns: Run[] = $derived(sortByCreatedAtDesc(runs));

    let selectedRunId = $derived.by(() => {
        if (userSelectedRunId && sortedRuns.some((r) => r.id === userSelectedRunId)) {
            return userSelectedRunId;
        }
        return sortedRuns[0]?.id ?? null;
    });

    let selectedRun = $derived(runs.find((r: Run) => r.id === selectedRunId));
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
        />

        <!-- Right Panel: Run Details -->
        <div
            class="flex flex-col overflow-hidden rounded-xl border border-slate-200 bg-white shadow-sm md:col-span-8 lg:col-span-9"
        >
            <RunDetailPanel run={selectedRun} {fetchLogs} {streamLogs} showTaskName />
        </div>
    </div>
</PageContainer>
