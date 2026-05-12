<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { ArrowRight, Box, Search } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import EmptyState from "@runwisp/ui/components/EmptyState.svelte";
    import Input from "@runwisp/ui/components/Input.svelte";
    import Select from "@runwisp/ui/components/Select.svelte";
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
    } from "./overview-format.js";

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
            toneClass: "bg-danger-50 text-danger-700",
        },
        running: {
            label: "Running now",
            badge: "primary",
            accentClass: "border-l-wisp-300",
            toneClass: "bg-wisp-50 text-wisp-700",
        },
        scheduled: {
            label: "Scheduled",
            badge: "info",
            accentClass: "border-l-aurora-300",
            toneClass: "bg-aurora-50 text-aurora-700",
        },
        manual: {
            label: "Manual only",
            badge: "warning",
            accentClass: "border-l-warning-300",
            toneClass: "bg-warning-50 text-warning-700",
        },
        idle: {
            label: "Idle",
            badge: "default",
            accentClass: "border-l-mist-200",
            toneClass: "bg-mist-100 text-mist-700",
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
            <h2 class="text-sm font-semibold text-mist-950">Tasks</h2>
            <span class="text-xs text-mist-500">
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
                <Search class="absolute top-1/2 left-3 -translate-y-1/2 text-mist-400" size={14} />
            </div>

            <div class="flex items-center gap-2">
                <div class="flex flex-wrap gap-1.5">
                    {#each filterOptions as filter (filter.value)}
                        <button
                            class={[
                                "inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs font-medium transition-all",
                                taskFilter === filter.value
                                    ? "border-wisp-200 bg-wisp-50 text-wisp-700"
                                    : "border-mist-200 bg-white text-mist-600 hover:border-mist-300 hover:text-mist-950",
                            ]}
                            onclick={() => (taskFilter = filter.value)}
                        >
                            {filter.label}
                            <span
                                class={[
                                    "rounded-md px-1.5 py-0.5 text-[10px] font-semibold",
                                    taskFilter === filter.value
                                        ? "bg-white text-wisp-700"
                                        : "bg-mist-100 text-mist-500",
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
            description="Tasks will appear here as soon as RunWisp loads its configuration."
            icon={Box}
        />
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

                <button
                    class="group w-full rounded-xl border border-l-4 border-mist-200 bg-white px-4 py-3 text-left transition-all hover:border-mist-300 hover:shadow-sm {taskState.accentClass}"
                    onclick={() => viewTask(task.task.name)}
                >
                    <div class="flex items-center gap-4">
                        <div class="min-w-0 flex-1">
                            <div class="flex flex-wrap items-center gap-1.5">
                                <span class="text-sm font-semibold text-mist-950">
                                    {task.task.name}
                                </span>
                                <Badge variant={taskState.badge} size="sm">{taskState.label}</Badge>
                                {#if task.task.group}
                                    <Badge variant="default" size="sm">{task.task.group}</Badge>
                                {/if}
                                {#if task.task.kind === "service"}
                                    <Badge variant="info" size="sm">
                                        {(task.task.instances ?? 1) > 1
                                            ? `Service ×${task.task.instances}`
                                            : "Service"}
                                    </Badge>
                                {/if}
                            </div>

                            <p class="mt-1 truncate text-xs text-mist-500">
                                {formatTaskDescription(task.task)}
                            </p>
                        </div>

                        <div class="hidden shrink-0 items-center gap-4 text-xs sm:flex">
                            <div class="w-28">
                                <p class="text-mist-400">Latest</p>
                                <span
                                    class={[
                                        "inline-flex rounded-full px-1.5 py-0.5 text-[10px] font-semibold",
                                        lastStatusConfig?.badge ?? taskState.toneClass,
                                    ]}
                                >
                                    {formatTaskLastResultLabel(task)}
                                </span>
                            </div>

                            <div class="w-32">
                                <p class="text-mist-400">Next run</p>
                                <p class="font-medium text-mist-950">
                                    {formatTaskNextRunLabel(task)}
                                </p>
                            </div>

                            <div class="w-24">
                                <p class="text-mist-400">Trigger</p>
                                <p
                                    class={[
                                        "font-medium text-mist-950",
                                        task.task.cron ? "font-mono" : "",
                                    ]}
                                >
                                    {formatTaskTriggerLabel(task)}
                                </p>
                            </div>
                        </div>

                        <ArrowRight
                            size={14}
                            class="shrink-0 text-mist-300 transition-colors group-hover:text-wisp-600"
                        />
                    </div>

                    {#if isFailureEndReason(task.lastRun?.end_reason)}
                        <div
                            class="mt-2 rounded-lg border border-danger-100 bg-danger-50/80 px-3 py-2 text-xs text-danger-700"
                        >
                            Last run exited with code {task.lastRun?.exit_code}
                        </div>
                    {/if}
                </button>
            {/each}
        </div>
    {/if}
</div>
