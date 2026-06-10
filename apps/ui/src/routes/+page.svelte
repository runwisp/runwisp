<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { goto } from "$app/navigation";
    import { resolve } from "$app/paths";
    import { OverviewPage, type DaemonState, type DaemonStats } from "$lib/components/dashboard";
    import { formatBytes } from "@runwisp/ui";
    import AsyncDataView from "$lib/components/AsyncDataView.svelte";
    import { runsApi, tasksApi, systemApi, type MetricsSample } from "$lib/api";
    import {
        runUpdatesStore,
        upsertRun,
        removeRun,
        connectionStore,
        systemStore,
    } from "$lib/stores";
    import { getApiUrl } from "$lib/utils/env";
    import { toTaskPageId } from "$lib/utils/task-id";
    import { AsyncData } from "$lib/utils/async-data.svelte";
    import { type Run, type Task } from "$lib/types";

    const RECENT_RUN_LIMIT = 16;
    const RUNNING_RUN_LIMIT = 8;
    const TASKS_REFRESH_DEBOUNCE_MS = 1000;

    interface DashboardState {
        tasks: Task[];
        recentRuns: Run[];
        runningRuns: Run[];
        metricsHistory: MetricsSample[];
    }

    let dashState = $state<DashboardState>({
        tasks: [],
        recentRuns: [],
        runningRuns: [],
        metricsHistory: [],
    });

    const pageData = new AsyncData(async () => {
        const [tasksData, recentRunsRes, runningRunsRes] = await Promise.all([
            tasksApi.getAll(),
            runsApi.getAll({
                limit: RECENT_RUN_LIMIT,
                sort_field: "start_at",
                sort_direction: "desc",
            }),
            runsApi.getAll({
                limit: RUNNING_RUN_LIMIT,
                status: "running",
                sort_field: "start_at",
                sort_direction: "desc",
            }),
        ]);
        return {
            tasks: tasksData,
            recentRuns: recentRunsRes.runs,
            runningRuns: runningRunsRes.runs,
        };
    });

    let daemonState = $derived<DaemonState>({
        name: systemStore.name,
        version: systemStore.version,
        uptime: systemStore.uptime,
        status: connectionStore.status === "connected" ? "connected" : "disconnected",
        host: systemStore.host,
        cpus: systemStore.cpus,
        memory: formatBytes(systemStore.memTotal),
        backendUrl: getApiUrl(),
        os: systemStore.os,
        arch: systemStore.arch,
        workDir: systemStore.workDir,
        fingerprint: systemStore.fingerprint,
    });

    let stats = $derived.by<DaemonStats>(() => {
        let completed = 0;
        let successes = 0;

        for (const r of dashState.recentRuns) {
            if (r.status === "ended") {
                completed++;
                if (r.end_reason === "success") successes++;
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

    $effect(() => {
        const unsubscribe = runUpdatesStore.subscribeToUpdates((event) => {
            if (event.type === "run.deleted") {
                dashState.recentRuns = removeRun(dashState.recentRuns, event.data.run_id);
                dashState.runningRuns = removeRun(dashState.runningRuns, event.data.run_id);
                return;
            }
            const run = event.data.run;

            dashState.recentRuns = upsertRun(dashState.recentRuns, run).slice(0, RECENT_RUN_LIMIT);
            dashState.runningRuns = upsertRunningRun(dashState.runningRuns, run, RUNNING_RUN_LIMIT);

            // A new run means the scheduler advanced that task's next_run_at —
            // refetch tasks so "Up next" and next-run columns stay current.
            // Pointless when the local scheduler is inactive (cloud mode):
            // next_run_at is always empty and that UI is hidden anyway.
            if (event.type === "run.created" && systemStore.schedulingActive) {
                scheduleTasksRefresh();
            }
        });

        const statsInterval = setInterval(() => void loadSystemStats(), 2000);

        void pageData.fetch();
        void loadSystemStats();

        return () => {
            unsubscribe();
            clearInterval(statsInterval);
            if (tasksRefreshTimer) {
                clearTimeout(tasksRefreshTimer);
                tasksRefreshTimer = null;
            }
        };
    });

    $effect(() => {
        if (pageData.data) {
            dashState.tasks = pageData.data.tasks;
            dashState.recentRuns = pageData.data.recentRuns;
            dashState.runningRuns = pageData.data.runningRuns;
        }
    });

    let tasksRefreshTimer: ReturnType<typeof setTimeout> | null = null;

    // Trailing debounce so a burst of simultaneous cron fires coalesces into
    // a single /api/tasks refetch.
    function scheduleTasksRefresh() {
        if (tasksRefreshTimer) return;
        tasksRefreshTimer = setTimeout(() => {
            tasksRefreshTimer = null;
            void refreshTasks();
        }, TASKS_REFRESH_DEBOUNCE_MS);
    }

    async function refreshTasks() {
        try {
            dashState.tasks = await tasksApi.getAll();
        } catch {
            // keep the stale list — connection loss is surfaced by connectionStore
        }
    }

    async function loadSystemStats() {
        await systemStore.refresh();
        if (connectionStore.status === "disconnected") return;
        try {
            dashState.metricsHistory = await systemApi.getMetricsHistory();
        } catch {
            // silent — metrics history is secondary
        }
    }

    function upsertRunningRun(list: Run[], next: Run, limit: number): Run[] {
        const without = list.filter((run) => run.id !== next.id);
        if (next.status !== "running") {
            return without.slice(0, limit);
        }

        return [next, ...without].slice(0, limit);
    }

    async function handleTaskClick(taskName: string) {
        await goto(resolve(`/tasks/${taskName}`));
    }

    async function handleRunClick(taskName: string, runId: string) {
        await goto(resolve(`/tasks/${taskName}?runId=${runId}`));
    }
</script>

<AsyncDataView data={pageData}>
    <OverviewPage
        state={daemonState}
        {stats}
        recentRuns={dashState.recentRuns}
        runningRuns={dashState.runningRuns}
        tasks={dashState.tasks.map((t) => ({ id: toTaskPageId(t.name), ...t }))}
        metricsHistory={dashState.metricsHistory}
        cloudMode={systemStore.cloudEnabled}
        schedulingActive={systemStore.schedulingActive}
        onViewAllRuns={() => goto(resolve("/runs"))}
        onTaskClick={handleTaskClick}
        onRunClick={handleRunClick}
    />
</AsyncDataView>
