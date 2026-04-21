<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { goto } from "$app/navigation";
    import { resolve } from "$app/paths";
    import { OverviewPage, type DaemonState, type DaemonStats } from "$lib/components/dashboard";
    import { formatBytes, Skeleton } from "@runwisp/ui";
    import ConnectionLostPanel from "$lib/components/ConnectionLostPanel.svelte";
    import { runsApi, tasksApi, systemApi, type MetricsSample } from "$lib/api";
    import { runUpdatesStore, upsertRun, connectionStore } from "$lib/stores";
    import { getApiUrl } from "$lib/utils/env";
    import { toTaskPageId } from "$lib/utils/task-id";
    import { createAsyncData } from "$lib/utils/async-data.svelte";
    import { type Run, type Task } from "$lib/types";

    const RECENT_RUN_LIMIT = 16;
    const RUNNING_RUN_LIMIT = 8;

    interface DashboardState {
        tasks: Task[];
        recentRuns: Run[];
        runningRuns: Run[];
        backendReachable: boolean;
        cpuUsage: number;
        memUsage: number;
        metricsHistory: MetricsSample[];
    }

    let dashState = $state<DashboardState>({
        tasks: [],
        recentRuns: [],
        runningRuns: [],
        backendReachable: true,
        cpuUsage: 0,
        memUsage: 0,
        metricsHistory: [],
    });

    const pageData = createAsyncData(async () => {
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

    let daemonState = $state<DaemonState>({
        name: "runwisp",
        version: "—",
        uptime: "—",
        status: "connected",
        host: "unknown",
        cpus: 0,
        memory: "—",
        backendUrl: getApiUrl(),
        os: "—",
        arch: "—",
        workDir: "—",
        fingerprint: "—",
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
            cpuUsage: dashState.cpuUsage,
            memUsage: dashState.memUsage,
        };
    });

    $effect(() => {
        const unsubscribe = runUpdatesStore.subscribeToUpdates((event) => {
            const run = event.data.run;

            dashState.recentRuns = upsertRun(dashState.recentRuns, run).slice(0, RECENT_RUN_LIMIT);
            dashState.runningRuns = upsertRunningRun(dashState.runningRuns, run, RUNNING_RUN_LIMIT);
        });

        const disposeReconnect = connectionStore.onReconnect(() => {
            void loadData();
            void loadSystemStats();
        });

        const statsInterval = setInterval(() => void loadSystemStats(), 2000);

        void loadData();
        void loadSystemStats();

        return () => {
            unsubscribe();
            disposeReconnect();
            clearInterval(statsInterval);
        };
    });

    // Keep the legacy `daemonState.status` in sync with the shared connection store
    // so the overview hero's health banner reflects reality.
    $effect(() => {
        daemonState.status = connectionStore.status === "connected" ? "connected" : "disconnected";
    });

    async function loadData() {
        await pageData.fetch();
        if (pageData.data) {
            dashState.tasks = pageData.data.tasks;
            dashState.recentRuns = pageData.data.recentRuns;
            dashState.runningRuns = pageData.data.runningRuns;
            dashState.backendReachable = true;
            connectionStore.markConnected();
        } else if (pageData.error) {
            dashState.backendReachable = false;
            connectionStore.markDisconnected(pageData.error);
        }
    }

    async function loadSystemStats() {
        if (connectionStore.status === "disconnected") return;
        try {
            const [sys, info, history] = await Promise.all([
                systemApi.getStats(),
                systemApi.getInfo(),
                systemApi.getMetricsHistory(),
            ]);
            daemonState.name = sys.name;
            daemonState.version = sys.version;
            daemonState.uptime = sys.uptime;
            daemonState.host = sys.host;
            daemonState.cpus = sys.cpu_cores;
            daemonState.memory = formatBytes(sys.mem_total);
            daemonState.os = sys.os;
            daemonState.arch = sys.arch;
            daemonState.workDir = sys.work_dir;
            daemonState.fingerprint = info.fingerprint;

            dashState.cpuUsage = sys.cpu_usage;
            dashState.memUsage = sys.mem_usage;
            dashState.metricsHistory = history;
        } catch {
            // silent — system stats are secondary
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

{#if pageData.loading && !pageData.data}
    <Skeleton rows={4} />
{:else if connectionStore.status !== "connected" && !pageData.data}
    <ConnectionLostPanel backendUrl={daemonState.backendUrl} />
{:else}
    <OverviewPage
        state={daemonState}
        {stats}
        recentRuns={dashState.recentRuns}
        runningRuns={dashState.runningRuns}
        tasks={dashState.tasks.map((t) => ({ id: toTaskPageId(t.name), ...t }))}
        metricsHistory={dashState.metricsHistory}
        onViewAllRuns={() => goto(resolve("/runs"))}
        onTaskClick={handleTaskClick}
        onRunClick={handleRunClick}
    />
{/if}
