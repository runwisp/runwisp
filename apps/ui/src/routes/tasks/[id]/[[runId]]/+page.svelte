<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { TaskPage } from "$lib/components/dashboard";
    import { toast, extractErrorMessage, ErrorState, RunsList, RunDetailPanel } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { tasksApi } from "$lib/api";
    import { taskStore } from "$lib/stores";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { createLiveRuns } from "$lib/utils/live-runs.svelte";
    import { navigateToRun } from "$lib/utils/run-url";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    let taskName = $derived($page.params.id ?? "");
    // The selected run lives in the path as an optional segment: /tasks/{name}/{runId}.
    let runIdParam = $derived($page.params.runId ?? null);

    // Deep link to a log line: ?line=N.
    let highlightLine = $derived.by(() => {
        const v = $page.url.searchParams.get("line");
        if (!v) return null;
        const n = Number(v);
        return Number.isFinite(n) ? n : null;
    });

    // Mirror the user-selected run into the address bar so the URL is a shareable
    // permalink; null (e.g. the selected run was deleted) drops back to /tasks/{name}.
    function selectRun(runId: string | null) {
        navigateToRun(
            $page.url,
            runId ? resolve(`/tasks/${taskName}/${runId}`) : resolve(`/tasks/${taskName}`),
        );
    }

    let triggering = $state(false);
    let selectRunId = $state<string | null>(null);

    const live = createLiveRuns(() => taskName);

    let filters = $state<RunsListFilters>(emptyRunFilters());

    $effect(() => {
        if (!taskName) return;
        live.source.setFilters({ ...filters, taskName: taskName });
    });

    const DEFAULT_CONCURRENCY_LIMIT = 1;
    let activeRunCount = $derived(live.source.items.filter((r) => r.status === "running").length);

    // Refetch the shared task list on open; the layout keeps it current on
    // reloads. AsyncData drives the load/error UI.
    const taskData = new AsyncData(() => taskStore.load());

    let task = $derived(taskStore.items.find((t) => t.name === taskName) ?? null);
    let concurrencyLimit = $derived(task?.maxConcurrent ?? DEFAULT_CONCURRENCY_LIMIT);
    let concurrencyReached = $derived(triggering || activeRunCount >= concurrencyLimit);

    $effect(() => {
        if (taskName) void taskData.fetch();
        return () => taskData.abort();
    });

    $effect(() => live.deepLink.resolve(taskName ? runIdParam : null, live.source.items));

    async function handleRun(params?: Record<string, string | null>) {
        triggering = true;
        try {
            const newRun = await tasksApi.triggerRun(taskName, params);
            live.source.upsert(newRun);
            selectRunId = newRun.id;
            toast.success(`Triggered "${taskName}"`);
        } catch (err) {
            toast.error(extractErrorMessage(err, `Failed to trigger "${taskName}"`));
        } finally {
            triggering = false;
        }
    }

    async function handleStop(runId: string) {
        try {
            await tasksApi.stopRun(runId);
            toast.success("Stopped run");
        } catch (err) {
            toast.error(extractErrorMessage(err, "Failed to stop run"));
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
            {live}
            bind:filters
            {concurrencyReached}
            {triggering}
            onRun={handleRun}
            onStop={handleStop}
            initialRunId={runIdParam}
            initialHighlightLine={highlightLine}
            {selectRunId}
            onSelectRun={selectRun}
        />
    {:else}
        <ErrorState message={'No task named "' + taskName + '" found.'} />
    {/if}
</AsyncDataView>
