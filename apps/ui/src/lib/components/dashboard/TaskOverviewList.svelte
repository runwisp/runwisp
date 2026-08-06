<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ArrowRight, Box, Search } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import EmptyState from "@runwisp/ui/components/EmptyState.svelte";
    import Input from "@runwisp/ui/components/Input.svelte";
    import Select from "@runwisp/ui/components/Select.svelte";
    import Tooltip from "@runwisp/ui/components/Tooltip.svelte";
    import ComposeBadge from "../ComposeBadge.svelte";
    import TaskHeldBadge from "../TaskHeldBadge.svelte";
    import TaskSourceBadge from "../TaskSourceBadge.svelte";
    import { getRunStatusConfig } from "@runwisp/ui";
    import { isFailureEndReason } from "@runwisp/common";
    import type {
        OverviewTaskFilter,
        OverviewTaskSortKey,
        OverviewTaskState,
        TaskOverview,
    } from "./overview.js";
    import {
        formatTaskDescription,
        formatTaskLastResultLabel,
        formatTaskNextRunLabel,
        formatTaskTriggerLabel,
        taskTriggerIsHumanizedCron,
    } from "./overview-format.js";
    import { taskIcon, taskTriggerTooltip } from "$lib/utils/task-icon";

    type TaskCounts = Record<OverviewTaskFilter, number>;
    type BadgeTone = "default" | "primary" | "success" | "warning" | "danger" | "info";

    interface FilterOption {
        value: OverviewTaskFilter;
        label: string;
    }

    interface SortOption {
        value: OverviewTaskSortKey;
        label: string;
    }

    interface TaskStateConfig {
        label: string;
        badge: BadgeTone;
        accentClass: string;
        toneClass: string;
    }

    const EMPTY_TASKS: TaskOverview[] = [];
    const EMPTY_COUNTS: TaskCounts = {
        all: 0,
        attention: 0,
        running: 0,
        scheduled: 0,
        manual: 0,
    };
    const EMPTY_FILTERS: FilterOption[] = [];
    const EMPTY_SORT_OPTIONS: SortOption[] = [];

    const TASK_STATE_CONFIG: Record<OverviewTaskState, TaskStateConfig> = {
        attention: {
            label: "Needs attention",
            badge: "danger",
            accentClass: "border-l-danger-300",
            toneClass: "bg-danger-soft text-danger-soft-text",
        },
        running: {
            label: "Running now",
            badge: "primary",
            accentClass: "border-l-wisp-300",
            toneClass: "bg-primary-soft text-primary-soft-text",
        },
        scheduled: {
            label: "Scheduled",
            badge: "info",
            accentClass: "border-l-aurora-300",
            toneClass: "bg-info-soft text-info-soft-text",
        },
        manual: {
            label: "Manual only",
            badge: "warning",
            accentClass: "border-l-warning-300",
            toneClass: "bg-warning-soft text-warning-soft-text",
        },
        idle: {
            label: "Idle",
            badge: "default",
            accentClass: "border-l-mist-200",
            toneClass: "bg-surface-sunken text-on-surface-muted",
        },
    };

    let {
        taskOverviews = EMPTY_TASKS,
        filteredTasks = EMPTY_TASKS,
        searchQuery = $bindable(),
        taskFilter = $bindable(),
        sortBy = $bindable(),
        taskCounts = EMPTY_COUNTS,
        filterOptions = EMPTY_FILTERS,
        sortOptions = EMPTY_SORT_OPTIONS,
        now = new Date(),
        schedulingActive = true,
        onTaskClick,
    } = $props<{
        taskOverviews?: TaskOverview[];
        filteredTasks?: TaskOverview[];
        searchQuery: string;
        taskFilter: OverviewTaskFilter;
        sortBy: OverviewTaskSortKey;
        taskCounts?: TaskCounts;
        filterOptions?: FilterOption[];
        sortOptions?: SortOption[];
        now?: Date;
        schedulingActive?: boolean;
        onTaskClick?: (taskName: string) => void;
    }>();

    function getTaskStateConfig(state: OverviewTaskState): TaskStateConfig {
        return TASK_STATE_CONFIG[state];
    }

    function viewTask(taskName: string): void {
        onTaskClick?.(taskName);
    }
</script>

<div class="space-y-4">
    <!-- Toolbar -->
    <div class="flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <div class="flex items-center gap-3">
            <h2 class="text-sm font-semibold text-on-surface">Tasks</h2>
            <span class="font-mono text-xs text-on-surface-muted tabular-nums">
                {taskOverviews.length} total
            </span>
        </div>

        <div class="flex flex-col gap-3 sm:flex-row sm:items-center">
            <div class="relative w-full sm:w-64">
                <Input
                    type="text"
                    placeholder="Search tasks..."
                    bind:value={searchQuery}
                    class="w-full pl-9 text-sm"
                />
                <Search
                    class="absolute top-1/2 left-3 -translate-y-1/2 text-on-surface-faint"
                    size={14}
                />
            </div>

            <div class="flex items-center gap-2">
                <div class="flex flex-wrap gap-1.5">
                    {#each filterOptions as filter (filter.value)}
                        <button
                            class={[
                                "inline-flex items-center gap-1.5 rounded-[3px] border px-2.5 py-1 font-mono text-xs font-medium ",
                                taskFilter === filter.value
                                    ? "border-primary-soft-border bg-primary-soft text-primary-soft-text"
                                    : "border-outline bg-surface-raised text-on-surface-muted hover:border-outline-hover hover:text-primary",
                            ]}
                            onclick={() => (taskFilter = filter.value)}
                        >
                            {filter.label}
                            <span
                                class={[
                                    "rounded-[3px] px-1.5 py-0.5 font-mono text-2xs font-semibold tabular-nums",
                                    taskFilter === filter.value
                                        ? "bg-surface-raised text-primary-soft-text"
                                        : "bg-surface-sunken text-on-surface-muted",
                                ]}
                            >
                                {taskCounts[filter.value]}
                            </span>
                        </button>
                    {/each}
                </div>

                <div class="w-40">
                    <Select bind:value={sortBy} options={sortOptions} />
                </div>
            </div>
        </div>
    </div>

    <!-- Task list -->
    {#if taskOverviews.length === 0}
        <EmptyState
            title="No tasks configured yet"
            description="Tasks are defined in your runwisp.toml — the daemon never edits them for you. Add one and restart the daemon:"
            icon={Box}
        >
            {#snippet actions()}
                <div class="flex flex-col items-center gap-3">
                    <pre
                        class="rounded-[4px] border border-outline bg-surface-sunken px-4 py-3 text-left font-mono text-xs text-on-surface-muted">[tasks.hello]
cron = "*/5 * * * *"
run  = "echo hello"</pre>
                    <a
                        href="https://docs.runwisp.com/configuration/tasks/"
                        target="_blank"
                        rel="noreferrer"
                        class="text-sm font-medium text-primary hover:underline"
                    >
                        Task configuration docs →
                    </a>
                </div>
            {/snippet}
        </EmptyState>
    {:else if filteredTasks.length === 0}
        <EmptyState
            title="No tasks match this view"
            description="Try clearing the search query or switching to a broader filter."
            icon={Search}
        />
    {:else}
        <div class="space-y-2">
            {#each filteredTasks as task (task.task.id)}
                {@const taskState = getTaskStateConfig(task.state)}
                {@const lastStatusConfig = task.lastStatus
                    ? getRunStatusConfig(task.lastStatus)
                    : undefined}
                {@const TaskIcon = taskIcon(task.task)}

                <button
                    class="group w-full rounded-[4px] border border-l-4 border-outline bg-surface-raised px-4 py-3 text-left hover:border-outline-hover hover:shadow-sm {taskState.accentClass}"
                    onclick={() => viewTask(task.task.name)}
                >
                    <div class="flex items-center gap-4">
                        <div class="min-w-0 flex-1">
                            <div class="flex flex-wrap items-center gap-1.5">
                                <TaskIcon
                                    size={14}
                                    class="shrink-0 text-on-surface-faint group-hover:text-primary"
                                    aria-hidden="true"
                                />
                                <span
                                    class="font-mono text-sm font-semibold text-on-surface"
                                    title={taskTriggerTooltip(task.task)}
                                >
                                    {task.task.name}
                                </span>
                                <Badge variant={taskState.badge} size="sm">{taskState.label}</Badge>
                                {#if task.task.group}
                                    <Badge variant="default" size="sm">{task.task.group}</Badge>
                                {/if}
                                {#if task.task.compose}
                                    <ComposeBadge
                                        file={task.task.compose.file}
                                        service={task.task.compose.service}
                                        projectName={task.task.compose.projectName}
                                    />
                                {/if}
                                {#if task.task.heldBy}
                                    <TaskHeldBadge heldBy={task.task.heldBy} />
                                {/if}
                                {#if task.task.source}
                                    <TaskSourceBadge
                                        name={task.task.name}
                                        source={task.task.source}
                                        sourceFile={task.task.sourceFile}
                                    />
                                {/if}
                                {#if task.task.kind === "service"}
                                    <Badge variant="info" size="sm">
                                        {(task.task.instances ?? 1) > 1
                                            ? `Service ×${task.task.instances}`
                                            : "Service"}
                                    </Badge>
                                {/if}
                            </div>

                            <p class="mt-1 truncate text-xs text-on-surface-muted">
                                {formatTaskDescription(task.task)}
                            </p>
                        </div>

                        <div class="hidden shrink-0 items-center gap-4 text-xs sm:flex">
                            <div class="w-28">
                                <p
                                    class="font-mono text-2xs tracking-[0.12em] text-on-surface-faint uppercase"
                                >
                                    Latest
                                </p>
                                {#if lastStatusConfig}
                                    <Tooltip
                                        content={lastStatusConfig.description}
                                        position="left"
                                        wide
                                    >
                                        <span
                                            class={[
                                                "inline-flex rounded-[3px] px-1.5 py-0.5 font-mono text-2xs font-semibold",
                                                lastStatusConfig.badge,
                                            ]}
                                        >
                                            {formatTaskLastResultLabel(task)}
                                        </span>
                                    </Tooltip>
                                {:else}
                                    <span
                                        class={[
                                            "inline-flex rounded-[3px] px-1.5 py-0.5 font-mono text-2xs font-semibold",
                                            taskState.toneClass,
                                        ]}
                                    >
                                        {formatTaskLastResultLabel(task)}
                                    </span>
                                {/if}
                            </div>

                            {#if schedulingActive}
                                <div class="w-32">
                                    <p
                                        class="font-mono text-2xs tracking-[0.12em] text-on-surface-faint uppercase"
                                    >
                                        Next run
                                    </p>
                                    <p
                                        class="font-mono text-xs font-medium text-on-surface tabular-nums"
                                    >
                                        {formatTaskNextRunLabel(task, now)}
                                    </p>
                                </div>

                                <div class="w-24">
                                    <p
                                        class="font-mono text-2xs tracking-[0.12em] text-on-surface-faint uppercase"
                                    >
                                        Trigger
                                    </p>
                                    {#if task.task.cron && task.task.kind !== "service"}
                                        <Tooltip
                                            content={taskTriggerTooltip(task.task)}
                                            position="left"
                                        >
                                            <p
                                                class={[
                                                    "font-medium text-on-surface",
                                                    taskTriggerIsHumanizedCron(task)
                                                        ? ""
                                                        : "font-mono",
                                                ]}
                                            >
                                                {formatTaskTriggerLabel(task)}
                                            </p>
                                        </Tooltip>
                                    {:else}
                                        <p class="font-mono font-medium text-on-surface">
                                            {formatTaskTriggerLabel(task)}
                                        </p>
                                    {/if}
                                </div>
                            {/if}
                        </div>

                        <ArrowRight
                            size={14}
                            class="shrink-0 text-on-surface-faint group-hover:text-primary"
                        />
                    </div>

                    {#if isFailureEndReason(task.lastRun?.endReason)}
                        <div
                            class="mt-2 rounded-[3px] border border-danger-soft-border bg-danger-soft/80 px-3 py-2 text-xs text-danger-soft-text"
                        >
                            Last run exited with code <span class="font-mono tabular-nums"
                                >{task.lastRun?.exitCode}</span
                            >
                        </div>
                    {/if}
                </button>
            {/each}
        </div>
    {/if}
</div>
