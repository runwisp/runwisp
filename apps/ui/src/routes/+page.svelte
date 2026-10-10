<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { untrack } from "svelte";
    import { goto } from "$app/navigation";
    import { resolve } from "$app/paths";
    import { OverviewPage, OverviewSkeleton } from "$lib/components/dashboard";
    import { RunMotion, type DaemonStats } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { runsApi } from "$lib/api";
    import { runUpdatesStore, systemStore, taskStore } from "$lib/stores";
    import { mergeRuns } from "$lib/utils/overview-runs";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import type { Run } from "@runwisp/common";

    const RECENT_RUN_LIMIT = 16;
    const RUNNING_RUN_LIMIT = 8;

    const isRunning = (run: Run) => run.status === "running";

    interface DashboardState {
        recentRuns: Run[];
        runningRuns: Run[];
        totalRuns: number;
    }

    let dashState = $state<DashboardState>({
        recentRuns: [],
        runningRuns: [],
        totalRuns: 0,
    });

    const pageData = new AsyncData(async () => {
        // Refetch tasks too: next-run times may have advanced while this page
        // was closed. The overview reads them from taskStore.
        const [, recentRunsRes, runningRunsRes] = await Promise.all([
            taskStore.load(),
            runsApi.getAll({
                limit: RECENT_RUN_LIMIT,
                sortField: "startedAt",
                sortDirection: "desc",
            }),
            runsApi.getAll({
                limit: RUNNING_RUN_LIMIT,
                status: "running",
                sortField: "startedAt",
                sortDirection: "desc",
            }),
        ]);
        return {
            recentRuns: recentRunsRes.runs,
            runningRuns: runningRunsRes.runs,
            // Unfiltered total → every run ever recorded, for the "total runs" pane.
            totalRuns: recentRunsRes.total,
        };
    });

    let stats = $derived.by<DaemonStats>(() => {
        let completed = 0;
        let successes = 0;

        for (const r of dashState.recentRuns) {
            if (r.status === "ended") {
                completed++;
                if (r.endReason === "succeeded") successes++;
            }
        }

        const successRate = completed > 0 ? (successes / completed) * 100 : 0;

        return {
            activeTasks: dashState.runningRuns.length,
            successRate: Math.round(successRate * 10) / 10,
            cpuUsage: systemStore.cpuUsage,
            memUsage: systemStore.memUsage,
        };
    });

    // Runs that finish while the page is open slide into Recent activity.
    const motion = new RunMotion();

    $effect(() => {
        const unsubscribe = runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                const { runId } = event.data;
                dashState.recentRuns = dashState.recentRuns.filter((r) => r.id !== runId);
                dashState.runningRuns = dashState.runningRuns.filter((r) => r.id !== runId);
                dashState.totalRuns = Math.max(0, dashState.totalRuns - 1);
                return;
            }
            const run = event.data.run;
            if (run.status === "ended") motion.markArrived(run.id);

            dashState.recentRuns = mergeRuns(dashState.recentRuns, [run], RECENT_RUN_LIMIT);
            dashState.runningRuns = mergeRuns(
                dashState.runningRuns,
                [run],
                RUNNING_RUN_LIMIT,
                isRunning,
            );

            // A new run means the scheduler advanced that task's nextRunAt,
            // refetch tasks so "Up next" and next-run columns stay current.
            // (tasks.changed is already handled by the layout.)
            // Pointless when the local scheduler is inactive (station mode):
            // nextRunAt is always empty and that UI is hidden anyway.
            if (event.type === "run.created") {
                dashState.totalRuns += 1;
                if (systemStore.schedulingActive) {
                    taskStore.refreshSoon();
                }
            }
        });

        void pageData.fetch();

        return () => {
            unsubscribe();
        };
    });

    $effect(() => {
        const data = pageData.data;
        if (data) {
            dashState.totalRuns = data.totalRuns;
            // Merge the snapshot through the same phase-order guard the SSE path
            // uses, so a fetch that resolves with an older view can't revert a
            // run the live stream already advanced (e.g. finished → running).
            // Read the current runs untracked: the merge folds them into itself,
            // so tracking them here would make this effect re-trigger on its own
            // writes and loop until Svelte aborts it (effect_update_depth_exceeded).
            untrack(() => {
                dashState.recentRuns = mergeRuns(
                    dashState.recentRuns,
                    data.recentRuns,
                    RECENT_RUN_LIMIT,
                );
                dashState.runningRuns = mergeRuns(
                    dashState.runningRuns,
                    data.runningRuns,
                    RUNNING_RUN_LIMIT,
                    isRunning,
                );
            });
        }
    });

    async function handleTaskClick(taskName: string) {
        await goto(resolve(`/tasks/${taskName}`));
    }

    async function handleRunClick(taskName: string, runId: string) {
        await goto(resolve(`/tasks/${taskName}/${runId}`));
    }
</script>

<AsyncDataView data={pageData}>
    {#snippet skeleton()}<OverviewSkeleton />{/snippet}
    <OverviewPage
        {stats}
        recentRuns={dashState.recentRuns}
        runningRuns={dashState.runningRuns}
        totalRuns={dashState.totalRuns}
        tasks={taskStore.items}
        onViewAllRuns={() => goto(resolve("/runs"))}
        onTaskClick={handleTaskClick}
        onRunClick={handleRunClick}
        {motion}
    />
</AsyncDataView>
