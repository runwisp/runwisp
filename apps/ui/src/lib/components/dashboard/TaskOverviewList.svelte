<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ArrowRight, Box, Search } from "@lucide/svelte";
    import RunStatusBadge from "./RunStatusBadge.svelte";
    import TaskBadges from "./TaskBadges.svelte";
    import TaskUsage from "../TaskUsage.svelte";
    import {
        RUN_STATUS_CONFIG,
        Badge,
        CodeBlock,
        EmptyState,
        Heading,
        Input,
        Link,
        Select,
        Tooltip,
    } from "@runwisp/ui";
    import type {
        OverviewTaskCounts,
        OverviewTaskFilter,
        OverviewTaskGroup,
        OverviewTaskSortKey,
        OverviewTaskState,
        TaskOverview,
    } from "./overview.js";
    import {
        failureExitCode,
        formatTaskLastResultLabel,
        formatTaskNextRunLabel,
        formatTaskTriggerLabel,
        taskTriggerIsHumanizedCron,
    } from "./overview-format.js";
    import { isService } from "@runwisp/common";
    import { hasCron, taskIcon, taskTriggerTooltip } from "$lib/utils/task";
    import { systemStore } from "$lib/stores";

    type BadgeTone = "default" | "primary" | "success" | "warning" | "danger" | "info";

    const exampleTask = '[tasks.hello]\ncron = "*/5 * * * *"\nrun  = "echo hello"';

    interface FilterOption {
        value: OverviewTaskFilter;
        label: string;
    }

    // A type alias (not an interface) so it stays assignable to Select's
    // index-signatured SelectOption.
    type SortOption = {
        value: OverviewTaskSortKey;
        label: string;
    };

    interface TaskStateConfig {
        label: string;
        badge: BadgeTone;
        accentClass: string;
        toneClass: string;
    }

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
        stopped: {
            label: "Stopped",
            badge: "warning",
            accentClass: "border-l-warning-300",
            toneClass: "bg-warning-soft text-warning-soft-text",
        },
        paused: {
            label: "Paused",
            badge: "warning",
            accentClass: "border-l-warning-300",
            toneClass: "bg-warning-soft text-warning-soft-text",
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
        taskOverviews,
        filteredTasks,
        searchQuery = $bindable(),
        taskFilter = $bindable(),
        sortBy = $bindable(),
        taskCounts,
        taskGroups,
        selectedGroup,
        onGroupChange,
        filterOptions,
        sortOptions,
        now,
        schedulingActive,
        onTaskClick,
    }: {
        taskOverviews: TaskOverview[];
        filteredTasks: TaskOverview[];
        searchQuery: string;
        taskFilter: OverviewTaskFilter;
        sortBy: OverviewTaskSortKey;
        taskCounts: OverviewTaskCounts;
        taskGroups: OverviewTaskGroup[];
        selectedGroup: string | undefined;
        onGroupChange: (group: string | undefined) => void;
        filterOptions: FilterOption[];
        sortOptions: SortOption[];
        now: Date;
        schedulingActive: boolean;
        onTaskClick: (taskName: string) => void;
    } = $props();

    const ALL_GROUPS = "";

    // One task group means no `group` in use: nothing to pick.
    let showGroups = $derived(taskGroups.length > 1);
    let groupOptions = $derived([
        { value: ALL_GROUPS, label: "All groups" },
        ...taskGroups.map((g) => ({ value: g.name, label: `${g.name} (${String(g.count)})` })),
    ]);

    function pickGroup(value: unknown) {
        onGroupChange(typeof value === "string" && value !== ALL_GROUPS ? value : undefined);
    }
</script>

<div class="space-y-4">
    <!-- Two rows that wrap as whole blocks: the pickers drop under the heading,
         and the search drops under the filter chips, so no control ends up
         stranded on a line of its own. -->
    <div class="space-y-3">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
            <Heading level={2} size="sm">Tasks</Heading>
            <span class="font-mono text-xs text-on-surface-muted tabular-nums">
                {taskOverviews.length} total
            </span>
            <div class="flex w-full flex-wrap gap-2 sm:ml-auto sm:w-auto">
                {#if showGroups}
                    <div class="min-w-40 flex-1 sm:w-44 sm:flex-none">
                        <Select
                            size="sm"
                            value={selectedGroup ?? ALL_GROUPS}
                            options={groupOptions}
                            onchange={pickGroup}
                        />
                    </div>
                {/if}
                <div class="min-w-40 flex-1 sm:w-40 sm:flex-none">
                    <Select size="sm" bind:value={sortBy} options={sortOptions} />
                </div>
            </div>
        </div>

        <div class="flex flex-wrap items-center gap-2">
            <div class="flex flex-wrap gap-1.5">
                {#each filterOptions as filter (filter.value)}
                    <button
                        class={[
                            "inline-flex h-8.5 items-center gap-1.5 rounded-[3px] border px-2.5 font-mono text-xs font-medium ",
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

            <Input
                type="text"
                size="sm"
                placeholder="Search tasks..."
                bind:value={searchQuery}
                class="min-w-48 flex-1"
            >
                {#snippet leadingIcon()}
                    <Search size={14} />
                {/snippet}
            </Input>
        </div>
    </div>

    {#if taskOverviews.length === 0}
        <EmptyState
            title="No tasks configured yet"
            description="Tasks are defined in your runwisp.toml; the daemon never edits them for you. Add one and restart the daemon:"
            icon={Box}
        >
            {#snippet actions()}
                <div class="flex flex-col items-center gap-3">
                    <CodeBlock
                        variant="surface"
                        size="sm"
                        copyable={false}
                        class="text-left"
                        code={exampleTask}
                    />
                    <Link
                        href="https://docs.runwisp.com/configuration/tasks/"
                        external
                        variant="primary"
                        class="text-sm font-medium"
                    >
                        Task configuration docs →
                    </Link>
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
            {#each filteredTasks as task (task.task.name)}
                {@const taskState = TASK_STATE_CONFIG[task.state]}
                {@const lastStatusConfig = task.lastStatus
                    ? RUN_STATUS_CONFIG[task.lastStatus]
                    : undefined}
                {@const TaskIcon = taskIcon(task.task)}
                {@const exitCode = failureExitCode(task.lastRun)}

                <button
                    class="group w-full rounded-[4px] border border-l-4 border-outline bg-surface-raised px-4 py-3 text-left hover:border-outline-hover hover:shadow-sm {taskState.accentClass}"
                    onclick={() => onTaskClick(task.task.name)}
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
                                <TaskBadges task={task.task} />
                                {#if isService(task.task.kind)}
                                    <Badge variant="info" size="sm">
                                        {formatTaskTriggerLabel(task)}
                                    </Badge>
                                {/if}
                                <TaskUsage task={task.task} />
                            </div>

                            <p class="mt-1 truncate text-xs text-on-surface-muted">
                                {task.task.description ??
                                    "No description yet. Open the task to review its execution details."}
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
                                        <RunStatusBadge tone={lastStatusConfig.badge}>
                                            {formatTaskLastResultLabel(task)}
                                        </RunStatusBadge>
                                    </Tooltip>
                                {:else}
                                    <RunStatusBadge tone={taskState.toneClass}>
                                        {formatTaskLastResultLabel(task)}
                                    </RunStatusBadge>
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
                                        {formatTaskNextRunLabel(task, now, systemStore.timezone)}
                                    </p>
                                </div>

                                <div class="w-24">
                                    <p
                                        class="font-mono text-2xs tracking-[0.12em] text-on-surface-faint uppercase"
                                    >
                                        Trigger
                                    </p>
                                    {#if hasCron(task.task) && !isService(task.task.kind)}
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

                    {#if exitCode !== undefined}
                        <div
                            class="mt-2 rounded-[3px] border border-danger-soft-border bg-danger-soft/80 px-3 py-2 text-xs text-danger-soft-text"
                        >
                            Last run exited with code <span class="font-mono tabular-nums">
                                {exitCode}
                            </span>
                        </div>
                    {/if}
                </button>
            {/each}
        </div>
    {/if}
</div>
