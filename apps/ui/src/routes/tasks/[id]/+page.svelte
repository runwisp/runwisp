<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { page } from "$app/stores";
    import { TaskPage } from "$lib/components/dashboard";
    import { toast, Skeleton, ErrorState } from "@runwisp/ui";
    import { tasksApi } from "$lib/api";
    import { runUpdatesStore, upsertRun } from "$lib/stores";
    import { createAsyncData } from "$lib/utils/async-data.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { type Run, type Task } from "$lib/types";

    let taskName = $derived($page.params.id ?? "");

    let task = $state<Task | null>(null);
    let runs = $state<Run[]>([]);
    let triggering = $state(false);
    let stopping = $state(false);
    let selectRunId = $state<string | null>(null);

    const DEFAULT_CONCURRENCY_LIMIT = 1;
    let activeRunCount = $derived(runs.filter((r) => r.status === "running").length);
    let concurrencyLimit = $derived(
        task?.execution?.concurrency?.limit ?? DEFAULT_CONCURRENCY_LIMIT,
    );
    let concurrencyReached = $derived(triggering || activeRunCount >= concurrencyLimit);

    const pageData = createAsyncData(
        async (
            signal: AbortSignal,
        ): Promise<{
            task: Task | null;
            runs: Run[];
        }> => {
            const allTasks = await tasksApi.getAll();
            if (signal.aborted) throw new DOMException("Aborted", "AbortError");

            const foundTask = allTasks.find((t) => t.name === taskName) || null;
            if (!foundTask) return { task: null, runs: [] };

            const runsRes = await tasksApi.getRuns(taskName, {
                limit: 50,
                sort_field: "start_at",
                sort_direction: "desc",
            });
            if (signal.aborted) throw new DOMException("Aborted", "AbortError");

            return { task: foundTask, runs: runsRes.runs };
        },
    );

    const logSession = createLogSession({
        findRun: (runId) => runs.find((r) => r.id === runId),
        getTaskName: (_run) => taskName,
    });

    $effect(() => {
        const unsubscribe = runUpdatesStore.subscribeToUpdates((event) => {
            if (event.data.run.task_name !== taskName) return;
            runs = upsertRun(runs, event.data.run);
        });
        return () => unsubscribe();
    });

    $effect(() => {
        void loadData();
        return () => pageData.abort();
    });

    async function loadData() {
        if (!taskName) return;
        await pageData.fetch();
        if (pageData.data) {
            task = pageData.data.task;
            runs = pageData.data.runs;
        }
    }

    async function handleRun() {
        if (!taskName) return;
        triggering = true;
        try {
            const newRun = await tasksApi.triggerRun(taskName);
            runs = upsertRun(runs, newRun);
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
</script>

{#if pageData.loading}
    <Skeleton rows={4} />
{:else if pageData.error}
    <ErrorState message={pageData.error} onRetry={loadData} retrying={pageData.loading} />
{:else if task}
    <TaskPage
        {task}
        {runs}
        {concurrencyReached}
        {triggering}
        {stopping}
        onRun={handleRun}
        onStop={handleStop}
        fetchLogs={logSession.fetchLogs}
        streamLogs={logSession.streamLogs}
        initialRunId={$page.url.searchParams.get("runId")}
        {selectRunId}
    />
{:else}
    <ErrorState message={'No task named "' + taskName + '" found.'} />
{/if}
