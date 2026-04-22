<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import PageContainer from "@runwisp/ui/components/PageContainer.svelte";
    import type { MetricsSample } from "$lib/api";
    import OverviewHero from "./OverviewHero.svelte";
    import OverviewSidePanels from "./OverviewSidePanels.svelte";
    import RecentActivityPanel from "./RecentActivityPanel.svelte";
    import TaskOverviewList from "./TaskOverviewList.svelte";
    import {
        buildOverviewSummary,
        buildTaskOverviews,
        countTaskOverviews,
        filterTaskOverviews,
        sortRunsByStartDesc,
        type OverviewTaskFilter,
        type OverviewTaskSortKey,
        type TaskOverview,
    } from "./overview.js";
    import { pluralize } from "./overview-format.js";
    import type { DaemonState, DaemonStats } from "@runwisp/ui";
    import type { Run, Task } from "@runwisp/common";

    const TASK_FILTERS: { value: OverviewTaskFilter; label: string }[] = [
        { value: "all", label: "All" },
        { value: "attention", label: "Attention" },
        { value: "running", label: "Running" },
        { value: "scheduled", label: "Scheduled" },
        { value: "manual", label: "Manual" },
    ];

    const SORT_OPTIONS: { value: OverviewTaskSortKey; label: string }[] = [
        { value: "attention", label: "Priority first" },
        { value: "last_activity", label: "Last activity" },
        { value: "next_run", label: "Next run" },
        { value: "name", label: "Name" },
    ];

    const ATTENTION_LIMIT = 4;
    const RUNNING_LIMIT = 4;
    const UPCOMING_LIMIT = 4;
    const RECENT_ACTIVITY_LIMIT = 6;

    type SystemHealth = {
        label: string;
        variant: "default" | "primary" | "success" | "warning" | "danger" | "info";
        detail: string;
    };

    let {
        state: daemonState,
        stats,
        recentRuns = [],
        runningRuns = [],
        tasks = [],
        metricsHistory = [],
        onViewAllRuns,
        onTaskClick,
        onRunClick,
    } = $props<{
        state: DaemonState;
        stats: DaemonStats;
        recentRuns?: Run[];
        runningRuns?: Run[];
        tasks?: (Task & { id: string })[];
        metricsHistory?: MetricsSample[];
        onViewAllRuns?: () => void;
        onTaskClick?: (taskName: string) => void;
        onRunClick?: (taskName: string, runId: string) => void;
    }>();

    let searchQuery = $state("");
    let taskFilter = $state<OverviewTaskFilter>("all");
    let sortBy = $state<OverviewTaskSortKey>("attention");

    let taskOverviews = $derived(buildTaskOverviews(tasks, recentRuns, runningRuns));
    let summary = $derived(buildOverviewSummary(taskOverviews, runningRuns));
    let taskCounts = $derived(countTaskOverviews(taskOverviews));
    let filteredTasks = $derived(
        filterTaskOverviews(taskOverviews, searchQuery, taskFilter, sortBy),
    );
    let attentionTasks = $derived(
        filterTaskOverviews(taskOverviews, "", "attention", "attention").slice(0, ATTENTION_LIMIT),
    );
    let runningNow = $derived(sortRunsByStartDesc(runningRuns).slice(0, RUNNING_LIMIT));
    let upcomingTasks = $derived(
        filterTaskOverviews(taskOverviews, "", "scheduled", "next_run").slice(0, UPCOMING_LIMIT),
    );
    let recentActivity = $derived(sortRunsByStartDesc(recentRuns).slice(0, RECENT_ACTIVITY_LIMIT));
    let completedRunsCount = $derived(
        recentRuns.filter((run: Run) => run.status === "ended").length,
    );
    let healthyTasksCount = $derived(
        taskOverviews.filter((task: TaskOverview) => task.state !== "attention").length,
    );

    let systemHealth = $derived.by<SystemHealth>(() => {
        if (daemonState.status !== "connected") {
            const endpoint =
                daemonState.backendUrl.trim() === ""
                    ? "this site's origin"
                    : daemonState.backendUrl;
            return {
                label: "Daemon offline",
                variant: "danger",
                detail: `Unable to reach ${endpoint}.`,
            };
        }

        if (summary.attentionTasks > 0) {
            return {
                label: `${summary.attentionTasks} task${pluralize(summary.attentionTasks)} need attention`,
                variant: "warning",
                detail: "Start with the attention column to inspect failures and interrupted work.",
            };
        }

        if (runningNow.length > 0) {
            return {
                label: `${runningNow.length} live run${pluralize(runningNow.length)}`,
                variant: "primary",
                detail: "Active executions are updating in real time.",
            };
        }

        return {
            label: "All clear",
            variant: "success",
            detail: "No active failures and the daemon is reachable.",
        };
    });
</script>

<PageContainer variant="wide" class="space-y-5">
    <OverviewHero
        {daemonState}
        {stats}
        {summary}
        {systemHealth}
        {completedRunsCount}
        {healthyTasksCount}
        {metricsHistory}
        {onViewAllRuns}
    />

    <section class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <OverviewSidePanels
            {attentionTasks}
            {runningNow}
            {upcomingTasks}
            {onTaskClick}
            {onRunClick}
        />

        <RecentActivityPanel {recentActivity} {onRunClick} {onViewAllRuns} />
    </section>

    <section class="rounded-xl border border-mist-200 bg-white p-5 shadow-sm">
        <TaskOverviewList
            {taskOverviews}
            {filteredTasks}
            bind:searchQuery
            bind:taskFilter
            bind:sortBy
            {taskCounts}
            filterOptions={TASK_FILTERS}
            sortOptions={SORT_OPTIONS}
            {onTaskClick}
        />
    </section>
</PageContainer>
