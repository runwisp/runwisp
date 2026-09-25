<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import type { MetricsSample } from "$lib/api";
    import OverviewHero from "./OverviewHero.svelte";
    import SystemResourcesPanel from "./SystemResourcesPanel.svelte";
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
    import { instanceCountResolver } from "./instance-count.js";
    import { TickingNow, PageContainer, Card } from "@runwisp/ui";
    import type { DaemonState, DaemonStats } from "@runwisp/ui";
    import type { Run, Task } from "@runwisp/common";

    const TASK_FILTERS: { value: OverviewTaskFilter; label: string }[] = [
        { value: "all", label: "All" },
        { value: "attention", label: "Attention" },
        { value: "running", label: "Running" },
        { value: "scheduled", label: "Scheduled" },
        { value: "manual", label: "Manual" },
    ];

    // Scheduling-owned filters (scheduled/manual) only make sense when the
    // local scheduler computes next-run times. In station mode the station owns
    // scheduling, so those are cleanly omitted rather than shown empty.
    const SCHEDULING_FILTERS = new Set<OverviewTaskFilter>(["scheduled", "manual"]);

    const SORT_OPTIONS: { value: OverviewTaskSortKey; label: string }[] = [
        { value: "attention", label: "Priority first" },
        { value: "last_activity", label: "Last activity" },
        { value: "next_run", label: "Next run" },
        { value: "name", label: "Name" },
    ];

    const ATTENTION_LIMIT = 4;
    const RUNNING_LIMIT = 4;
    const RECENT_ACTIVITY_LIMIT = 6;

    let {
        state: daemonState,
        stats,
        recentRuns = [],
        runningRuns = [],
        totalRuns = 0,
        tasks = [],
        metricsHistory = [],
        stationMode = false,
        schedulingActive = true,
        onViewAllRuns,
        onTaskClick,
        onRunClick,
    } = $props<{
        state: DaemonState;
        stats: DaemonStats;
        recentRuns?: Run[];
        runningRuns?: Run[];
        totalRuns?: number;
        tasks?: (Task & { id: string })[];
        metricsHistory?: MetricsSample[];
        stationMode?: boolean;
        schedulingActive?: boolean;
        onViewAllRuns?: () => void;
        onTaskClick?: (taskName: string) => void;
        onRunClick?: (taskName: string, runId: string) => void;
    }>();

    let searchQuery = $state("");
    let taskFilter = $state<OverviewTaskFilter>("all");
    let sortBy = $state<OverviewTaskSortKey>("attention");

    // When the local scheduler is inactive, scheduling-owned controls are
    // hidden. Reset any selection that points at one so the view stays
    // coherent if schedulingActive flips false after first /api/daemon.
    $effect(() => {
        if (schedulingActive) return;
        if (SCHEDULING_FILTERS.has(taskFilter)) taskFilter = "all";
        if (sortBy === "next_run") sortBy = "attention";
    });

    let taskFilters = $derived(
        schedulingActive
            ? TASK_FILTERS
            : TASK_FILTERS.filter((filter) => !SCHEDULING_FILTERS.has(filter.value)),
    );
    let sortOptions = $derived(
        schedulingActive
            ? SORT_OPTIONS
            : SORT_OPTIONS.filter((option) => option.value !== "next_run"),
    );

    // Tick every 30s so relative-time labels ("in 2 hours") stay current
    // while the page sits open.
    const ticker = new TickingNow();
    $effect(() => ticker.start());

    let getInstanceCount = $derived(instanceCountResolver(tasks));
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
        schedulingActive ? filterTaskOverviews(taskOverviews, "", "scheduled", "next_run") : [],
    );
    // Exclude in-flight runs — they already have their own "Running now" pane;
    // Recent activity is for finished work.
    let recentActivity = $derived(
        sortRunsByStartDesc(recentRuns.filter((run: Run) => run.status !== "running")).slice(
            0,
            RECENT_ACTIVITY_LIMIT,
        ),
    );
    let completedRunsCount = $derived(
        recentRuns.filter((run: Run) => run.status === "ended").length,
    );
    let healthyTasksCount = $derived(
        taskOverviews.filter((task: TaskOverview) => task.state !== "attention").length,
    );
</script>

<PageContainer variant="wide" class="space-y-5">
    <!-- One grid so the left column (stat panes → attention/running/up-next)
         flows straight into the panels with no gap under the short stat row,
         while the 320px rail (resources → recent activity) tracks alongside. -->
    <div class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
        <div class="flex flex-col gap-5">
            <OverviewHero
                {stats}
                {summary}
                {totalRuns}
                {completedRunsCount}
                {healthyTasksCount}
                uptime={daemonState.uptime}
                {stationMode}
            />

            <OverviewSidePanels
                {attentionTasks}
                {runningNow}
                {upcomingTasks}
                showUpcoming={schedulingActive}
                now={ticker.now}
                {onTaskClick}
                {onRunClick}
                {getInstanceCount}
            />
        </div>

        <div class="flex flex-col gap-5">
            <SystemResourcesPanel {stats} {metricsHistory} />

            <RecentActivityPanel
                {recentActivity}
                now={ticker.now}
                {onRunClick}
                {onViewAllRuns}
                {getInstanceCount}
            />
        </div>
    </div>

    <Card padding="lg">
        <TaskOverviewList
            {taskOverviews}
            {filteredTasks}
            now={ticker.now}
            bind:searchQuery
            bind:taskFilter
            bind:sortBy
            {taskCounts}
            filterOptions={taskFilters}
            {sortOptions}
            {schedulingActive}
            {onTaskClick}
        />
    </Card>
</PageContainer>
