<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { RunsPage } from "$lib/components/dashboard";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { runsApi } from "$lib/api";
    import { runUpdatesStore, upsertRun, removeRun } from "$lib/stores";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { createLogSession } from "$lib/utils/log-session";
    import { type Run } from "$lib/types";

    let runs = $state<Run[]>([]);

    const runsData = new AsyncData(() =>
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
            if (event.type === "run.deleted") {
                runs = removeRun(runs, event.data.run_id);
                return;
            }
            runs = upsertRun(runs, event.data.run);
        });
        void runsData.fetch();
        return () => unsubscribe();
    });

    $effect(() => {
        if (runsData.data) {
            runs = runsData.data.runs;
        }
    });
</script>

<AsyncDataView data={runsData} skeletonRows={5}>
    <RunsPage bind:runs fetchLogs={logSession.fetchLogs} streamLogs={logSession.streamLogs} />
</AsyncDataView>
