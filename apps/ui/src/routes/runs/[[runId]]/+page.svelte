<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { page } from "$app/stores";
    import { resolve } from "$app/paths";
    import { RunsPage } from "$lib/components/dashboard";
    import { instanceCountResolver } from "$lib/components/dashboard/instance-count";
    import { Skeleton } from "@runwisp/ui";
    import { runsApi } from "$lib/api";
    import { runUpdatesStore, taskStore } from "$lib/stores";
    import { createRunsSource } from "$lib/utils/runs-source.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { navigateToRun } from "$lib/utils/run-url";
    import { emptyRunFilters, type RunsListFilters } from "@runwisp/ui";

    const source = createRunsSource();

    // The selected run lives in the path as an optional segment: /runs/{runId}.
    let runIdParam = $derived($page.params.runId ?? null);

    // Mirror the user-selected run into the address bar so the URL is shareable;
    // null drops back to /runs.
    function selectRun(runId: string | null) {
        navigateToRun($page.url, runId ? resolve(`/runs/${runId}`) : resolve("/runs"));
    }

    let getInstanceCount = $derived(instanceCountResolver(taskStore.items));

    let filters = $state<RunsListFilters>(emptyRunFilters());

    $effect(() => {
        source.setFilters({ ...filters });
    });

    const logSession = createLogSession({
        findRun: (runId) => source.items.find((r) => r.id === runId),
        getTaskName: (run) => run.task_name,
    });

    $effect(() => {
        return runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                source.remove(event.data.run_id);
                return;
            }
            source.upsert(event.data.run);
        });
    });

    // Whether the deep-linked run id resolved to no run. Surfaced to RunsPage so
    // a dead permalink shows a "not found" panel instead of silently swapping in
    // the newest run.
    let runNotFound = $state(false);
    let checkedMissingId = $state<string | null>(null);

    // Restore a deep-linked run (/runs/{runId}) that isn't on the loaded page.
    // The run ULID is globally unique, so we can fetch it without a task name.
    $effect(() => {
        const initialRunId = runIdParam;
        if (!initialRunId) {
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
                const run = await runsApi.getById(initialRunId);
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
</script>

{#if !source.loaded}
    <Skeleton rows={5} />
{:else}
    <RunsPage
        items={source.items}
        total={source.total}
        loading={source.loading}
        onLoadMore={() => source.loadMore()}
        bind:filters
        onOptimisticRemove={(ids) => ids.forEach((id) => source.remove(id))}
        onOptimisticRestore={(runs) => runs.forEach((run) => source.upsert(run))}
        fetchLogs={logSession.fetchLogs}
        streamLogs={logSession.streamLogs}
        fetchLineHistory={logSession.fetchLineHistory}
        {getInstanceCount}
        initialRunId={runIdParam}
        {runNotFound}
        onSelectRun={selectRun}
    />
{/if}
