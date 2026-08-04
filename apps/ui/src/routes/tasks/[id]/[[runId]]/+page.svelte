<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { TaskPage } from "$lib/components/dashboard";
    import { toast, ErrorState, Skeleton } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { tasksApi } from "$lib/api";
    import { runUpdatesStore, systemStore } from "$lib/stores";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { createRunsSource } from "$lib/utils/runs-source.svelte";
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

    const source = createRunsSource();

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

    const logSession = createLogSession({
        findRun: (runId) => source.items.find((r) => r.id === runId),
        getTaskName: (_run) => taskName,
    });

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                if (event.data.taskName !== taskName) return;
                source.remove(event.data.runId);
                return;
            }
            if (event.data.run.taskName !== taskName) return;
            source.upsert(event.data.run);
        });
    });

    $effect(() => {
        if (taskName) void taskData.fetch();
        return () => taskData.abort();
    });

    // Whether the deep-linked run id resolved to no run under this task. Surfaced
    // to TaskPage so a dead permalink shows a "not found" panel instead of quietly
    // selecting the running/newest run.
    let runNotFound = $state(false);
    let checkedMissingId = $state<string | null>(null);

    $effect(() => {
        const initialRunId = runIdParam;
        if (!initialRunId || !taskName) {
            runNotFound = false;
            checkedMissingId = null;
            return;
        }
        if (source.items.length === 0) return;
        if (source.items.some((r) => r.id === initialRunId)) {
            runNotFound = false;
            return;
        }
        if (checkedMissingId === initialRunId) return; // already resolved as missing
        // A genuinely new id that isn't loaded yet: clear any stale not-found
        // latched by a previous dead link so it doesn't flash "Run not found"
        // for this (possibly valid) run while the fetch is in flight.
        runNotFound = false;
        void (async () => {
            try {
                const run = await tasksApi.getRun(taskName, initialRunId);
                if (run) {
                    source.upsert(run);
                    return;
                }
            } catch {
                // Fall through: not found / not authorized is treated as missing.
            }
            checkedMissingId = initialRunId;
            runNotFound = true;
        })();
    });

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
            await tasksApi.stopRun(taskName, runId);
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
    {#if task}
        {#if !source.loaded}
            <Skeleton rows={5} />
        {:else}
            <TaskPage
                {task}
                cloudMode={systemStore.cloudEnabled}
                items={source.items}
                total={source.total}
                loading={source.loading}
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
                initialRunId={runIdParam}
                initialHighlightLine={(() => {
                    const v = $page.url.searchParams.get("line");
                    if (!v) return null;
                    const n = Number(v);
                    return Number.isFinite(n) ? n : null;
                })()}
                {selectRunId}
                {runNotFound}
                onSelectRun={selectRun}
            />
        {/if}
    {:else}
        <ErrorState message={'No task named "' + taskName + '" found.'} />
    {/if}
</AsyncDataView>
