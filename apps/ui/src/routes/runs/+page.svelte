<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { RunsPage } from "$lib/components/dashboard";
    import { Skeleton, ErrorState } from "@runwisp/ui";
    import { runsApi } from "$lib/api";
    import { runUpdatesStore, upsertRun } from "$lib/stores";
    import { createAsyncData } from "$lib/utils/async-data.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { type Run } from "$lib/types";

    let runs = $state<Run[]>([]);

    const runsData = createAsyncData(() =>
        runsApi.getAll({
            limit: 200,
            sort_field: "start_at",
            sort_direction: "desc",
        }),
    );

    const logSession = createLogSession({
        findRun: (runId) => runs.find((r) => r.id === runId),
        getTaskName: (run) => run.task_name,
    });

    $effect(() => {
        const unsubscribe = runUpdatesStore.subscribeToUpdates((event) => {
            runs = upsertRun(runs, event.data.run);
        });
        void loadRuns();
        return () => unsubscribe();
    });

    async function loadRuns() {
        await runsData.fetch();
        if (runsData.data) {
            runs = runsData.data.runs;
        }
    }
</script>

{#if runsData.loading}
    <Skeleton rows={5} />
{:else if runsData.error}
    <ErrorState message={runsData.error} onRetry={loadRuns} retrying={runsData.loading} />
{:else}
    <RunsPage {runs} fetchLogs={logSession.fetchLogs} streamLogs={logSession.streamLogs} />
{/if}
