<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { TaskPage } from "$lib/components/dashboard";
    import { toast, ErrorState, RunsList, RunDetailPanel } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { tasksApi } from "$lib/api";
    import { appEventStream } from "$lib/stores";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { createLiveRuns } from "$lib/utils/live-runs.svelte";
    import { navigateToRun } from "$lib/utils/run-url";
    import { type Task } from "$lib/types";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    let taskName = $derived($page.params.id ?? "");
    // The selected run lives in the path as an optional segment: /tasks/{name}/{runId}.
    let runIdParam = $derived($page.params.runId ?? null);

    // Mirror the user-selected run into the address bar so the URL is a shareable
    // permalink; null (e.g. the selected run was deleted) drops back to /tasks/{name}.
    function selectRun(runId: string | null) {
        navigateToRun(
            $page.url,
            runId ? resolve(`/tasks/${taskName}/${runId}`) : resolve(`/tasks/${taskName}`),
        );
    }

    let triggering = $state(false);
    let restarting = $state(false);
    let stoppingService = $state(false);
    let serviceStopped = $state(false);
    let selectRunId = $state<string | null>(null);

    const { source, logSession, deepLink } = createLiveRuns(() => taskName);

    let filters = $state<RunsListFilters>(emptyRunFilters());

    $effect(() => {
        if (!taskName) return;
        source.setFilters({ ...filters, taskName: taskName });
    });

    const DEFAULT_CONCURRENCY_LIMIT = 1;
    let activeRunCount = $derived(source.items.filter((r) => r.status === "running").length);

    const taskData = new AsyncData(async (signal: AbortSignal): Promise<Task | null> => {
        const allTasks = await tasksApi.getAll();
        if (signal.aborted) throw new DOMException("Aborted", "AbortError");
        return allTasks.find((t) => t.name === taskName) || null;
    });

    let task = $derived(taskData.data ?? null);
    let concurrencyLimit = $derived(task?.maxConcurrent ?? DEFAULT_CONCURRENCY_LIMIT);
    let concurrencyReached = $derived(triggering || activeRunCount >= concurrencyLimit);

    $effect(() => {
        if (taskName) void taskData.fetch();
        return () => taskData.abort();
    });

    // A reload can change this task's definition without touching its runs.
    $effect(() => appEventStream.subscribe("tasks.changed", () => void taskData.fetch()));

    $effect(() => deepLink.resolve(taskName ? runIdParam : null, source.items));

    async function handleRun(params?: Record<string, string | null>) {
        if (!taskName) return;
        triggering = true;
        try {
            const newRun = await tasksApi.triggerRun(taskName, params);
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
        try {
            await tasksApi.stopRun(runId);
            toast.success(`Stopped run`);
        } catch {
            toast.error(`Failed to stop run`);
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
    {#snippet skeleton()}
        <!-- The task page's own rail and panel, in their loading states. -->
        <div class="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col md:flex-row">
            <RunsList items={[]} total={0} loading filters={emptyRunFilters()} />
            <RunDetailPanel run={undefined} loading fetchLogs={() => undefined} />
        </div>
    {/snippet}
    {#if task}
        <TaskPage
            {task}
            items={source.items}
            total={source.total}
            loading={source.loading || !source.loaded}
            bind:filters
            onLoadMore={() => source.loadMore()}
            onOptimisticRemove={(ids) => ids.forEach((id) => source.remove(id))}
            onOptimisticRestore={(runs) => runs.forEach((run) => source.upsert(run))}
            {concurrencyReached}
            {triggering}
            {restarting}
            {stoppingService}
            {serviceStopped}
            onRun={handleRun}
            onStop={handleStop}
            onRestart={handleRestart}
            onStopService={handleStopService}
            fetchLogs={logSession.fetchLogs}
            streamLogs={logSession.streamLogs}
            fetchLineHistory={logSession.fetchLineHistory}
            motion={source.motion}
            initialRunId={runIdParam}
            initialHighlightLine={(() => {
                const v = $page.url.searchParams.get("line");
                if (!v) return null;
                const n = Number(v);
                return Number.isFinite(n) ? n : null;
            })()}
            {selectRunId}
            runNotFound={deepLink.notFound}
            runPending={deepLink.pending}
            onSelectRun={selectRun}
        />
    {:else}
        <ErrorState message={'No task named "' + taskName + '" found.'} />
    {/if}
</AsyncDataView>
