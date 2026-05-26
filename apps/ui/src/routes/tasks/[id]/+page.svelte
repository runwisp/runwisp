<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { page } from "$app/stores";
    import { TaskPage } from "$lib/components/dashboard";
    import { toast, ErrorState, Skeleton } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { tasksApi } from "$lib/api";
    import { runUpdatesStore } from "$lib/stores";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { createRunsSource } from "$lib/utils/runs-source.svelte";
    import { type Task } from "$lib/types";
    import type { RunsListFilters } from "@runwisp/ui";

    let taskName = $derived($page.params.id ?? "");

    let triggering = $state(false);
    let stopping = $state(false);
    let restarting = $state(false);
    let stoppingService = $state(false);
    let serviceStopped = $state(false);
    let selectRunId = $state<string | null>(null);

    const source = createRunsSource();

    let filters = $state<RunsListFilters>({
        search: "",
        status: "all",
        sort_direction: "desc",
    });

    $effect(() => {
        if (!taskName) return;
        source.setFilters({ ...filters, task_name: taskName });
    });

    const DEFAULT_CONCURRENCY_LIMIT = 1;
    let activeRunCount = $derived(source.items.filter((r) => r.status === "running").length);

    const taskData = new AsyncData(async (signal: AbortSignal): Promise<Task | null> => {
        const allTasks = await tasksApi.getAll();
        if (signal.aborted) throw new DOMException("Aborted", "AbortError");
        return allTasks.find((t) => t.name === taskName) || null;
    });

    let task = $derived(taskData.data ?? null);
    let concurrencyLimit = $derived(task?.max_concurrent ?? DEFAULT_CONCURRENCY_LIMIT);
    let concurrencyReached = $derived(triggering || activeRunCount >= concurrencyLimit);

    const logSession = createLogSession({
        findRun: (runId) => source.items.find((r) => r.id === runId),
        getTaskName: (_run) => taskName,
    });

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                if (event.data.task_name !== taskName) return;
                source.remove(event.data.run_id);
                return;
            }
            if (event.data.run.task_name !== taskName) return;
            source.upsert(event.data.run);
        });
    });

    $effect(() => {
        if (taskName) void taskData.fetch();
        return () => taskData.abort();
    });

    $effect(() => {
        const initialRunId = $page.url.searchParams.get("runId");
        if (!initialRunId || !taskName || source.items.length === 0) return;
        if (source.items.some((r) => r.id === initialRunId)) return;
        void (async () => {
            try {
                const run = await tasksApi.getRun(taskName, initialRunId);
                if (run) source.upsert(run);
            } catch {
                // Run not found / not authorized — TaskPage's fallback selection
                // (running run, else newest) keeps the UI usable.
            }
        })();
    });

    async function handleRun() {
        if (!taskName) return;
        triggering = true;
        try {
            const newRun = await tasksApi.triggerRun(taskName);
            source.upsert(newRun);
            selectRunId = newRun.id;
            toast.success(`Triggered "${taskName}"`);
        } catch {
            toast.error(`Failed to trigger "${taskName}"`);
        } finally {
            triggering = false;
        }
    }

    async function handleStop(runId: string) {
        if (!taskName) return;
        stopping = true;
        try {
            await tasksApi.stopRun(taskName, runId);
            toast.success(`Stopped run`);
        } catch {
            toast.error(`Failed to stop run`);
        } finally {
            stopping = false;
        }
    }

    async function handleRestart() {
        if (!taskName) return;
        restarting = true;
        try {
            await tasksApi.restartService(taskName);
            serviceStopped = false;
            toast.success(`Restarting "${taskName}"`);
        } catch {
            toast.error(`Failed to restart "${taskName}"`);
        } finally {
            restarting = false;
        }
    }

    async function handleStopService() {
        if (!taskName) return;
        stoppingService = true;
        try {
            await tasksApi.stopService(taskName);
            serviceStopped = true;
            toast.success(`Stopped "${taskName}"`);
        } catch {
            toast.error(`Failed to stop "${taskName}"`);
        } finally {
            stoppingService = false;
        }
    }
</script>

<AsyncDataView data={taskData}>
    {#if task}
        {#if source.items.length === 0 && source.loading}
            <Skeleton rows={5} />
        {:else}
            <TaskPage
                {task}
                items={source.items}
                total={source.total}
                loading={source.loading}
                bind:filters
                onLoadMore={() => source.loadMore()}
                onOptimisticRemove={(ids) => ids.forEach((id) => source.remove(id))}
                onOptimisticRestore={(runs) => runs.forEach((run) => source.upsert(run))}
                {concurrencyReached}
                {triggering}
                {stopping}
                {restarting}
                {stoppingService}
                {serviceStopped}
                onRun={handleRun}
                onStop={handleStop}
                onRestart={handleRestart}
                onStopService={handleStopService}
                fetchLogs={logSession.fetchLogs}
                streamLogs={logSession.streamLogs}
                initialRunId={$page.url.searchParams.get("runId")}
                initialHighlightLine={(() => {
                    const v = $page.url.searchParams.get("line");
                    if (!v) return null;
                    const n = Number(v);
                    return Number.isFinite(n) ? n : null;
                })()}
                {selectRunId}
            />
        {/if}
    {:else}
        <ErrorState message={'No task named "' + taskName + '" found.'} />
    {/if}
</AsyncDataView>
