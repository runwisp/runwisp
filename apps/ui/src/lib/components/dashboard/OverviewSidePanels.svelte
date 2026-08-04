<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { Activity, ArrowRight, Clock3, ShieldAlert, ShieldCheck } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import ComposeBadge from "../ComposeBadge.svelte";
    import TaskSourceBadge from "../TaskSourceBadge.svelte";
    import { getRunStatusConfig, TaskCard, instanceSuffix } from "@runwisp/ui";
    import type { TaskOverview } from "./overview.js";
    import { isFailureEndReason, type Run } from "@runwisp/common";
    import {
        formatRunDurationLabel,
        formatStatusLabel,
        formatTaskLastRunLabel,
        formatTaskNextRunLabel,
        formatTriggeredByLabel,
    } from "./overview-format.js";

    let {
        attentionTasks = [],
        runningNow = [],
        upcomingTasks = [],
        showUpcoming = true,
        now = new Date(),
        onTaskClick,
        onRunClick,
        getInstanceCount = () => 1,
    } = $props<{
        attentionTasks?: TaskOverview[];
        runningNow?: Run[];
        upcomingTasks?: TaskOverview[];
        showUpcoming?: boolean;
        now?: Date;
        onTaskClick?: (taskName: string) => void;
        onRunClick?: (taskName: string, runId: string) => void;
        getInstanceCount?: (taskName: string) => number;
    }>();

    function viewTask(taskName: string): void {
        onTaskClick?.(taskName);
    }

    function viewRun(run: Run): void {
        onRunClick?.(run.task_name, run.id);
    }
</script>

<div class={["grid gap-4", showUpcoming ? "lg:grid-cols-3" : "lg:grid-cols-2"]}>
    <!-- Needs attention -->
    <Card>
        <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-on-surface">Needs attention</h3>
            <Badge variant={attentionTasks.length > 0 ? "danger" : "success"}>
                {attentionTasks.length}
            </Badge>
        </div>

        {#if attentionTasks.length === 0}
            <div class="mt-4 flex items-center gap-2 rounded-lg bg-success-soft px-3 py-2.5">
                <ShieldCheck size={14} class="text-success-soft-text" />
                <span class="text-sm text-success-soft-text">Nothing waiting for triage</span>
            </div>
        {:else}
            <div class="mt-4 space-y-2">
                {#each attentionTasks as task (task.task.id)}
                    {@const statusConfig = task.lastStatus
                        ? getRunStatusConfig(task.lastStatus)
                        : undefined}

                    <TaskCard accent="danger" onclick={() => viewTask(task.task.name)}>
                        <div class="flex items-start justify-between gap-2">
                            <div class="min-w-0 flex-1">
                                <div class="flex flex-wrap items-center gap-1.5">
                                    <span class="truncate text-sm font-medium text-on-surface">
                                        {task.task.name}
                                    </span>
                                    {#if statusConfig}
                                        <span
                                            class="rounded-full px-1.5 py-0.5 text-2xs font-semibold uppercase {statusConfig.badge}"
                                        >
                                            {formatStatusLabel(task.lastStatus ?? "")}
                                        </span>
                                    {/if}
                                </div>
                                <p class="mt-1 text-xs text-on-surface-muted">
                                    Last run {formatTaskLastRunLabel(task, now)}
                                </p>
                            </div>
                            <ShieldAlert size={14} class="shrink-0 text-danger-soft-text" />
                        </div>

                        <div
                            class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-on-surface-muted"
                        >
                            <span>Next {formatTaskNextRunLabel(task, now)}</span>
                            {#if isFailureEndReason(task.lastRun?.end_reason)}
                                <span class="text-danger-soft-text">
                                    Exit {task.lastRun?.exit_code}
                                </span>
                            {/if}
                        </div>
                    </TaskCard>
                {/each}
            </div>
        {/if}
    </Card>

    <!-- Running now -->
    <Card>
        <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-on-surface">Running now</h3>
            <Badge variant={runningNow.length > 0 ? "primary" : "default"}>
                {runningNow.length}
            </Badge>
        </div>

        {#if runningNow.length === 0}
            <div class="mt-4 flex items-center gap-2 rounded-lg bg-surface-sunken px-3 py-2.5">
                <Activity size={14} class="text-on-surface-muted" />
                <span class="text-sm text-on-surface-muted">Nothing is running</span>
            </div>
        {:else}
            <div class="mt-4 space-y-2">
                {#each runningNow as run (run.id)}
                    {@const suffix = instanceSuffix(
                        run.instance_index,
                        getInstanceCount(run.task_name),
                    )}
                    <TaskCard accent="wisp" onclick={() => viewRun(run)}>
                        <div class="flex items-start justify-between gap-2">
                            <div class="min-w-0 flex-1">
                                <div class="flex items-center gap-2">
                                    <span class="relative flex h-2 w-2 shrink-0">
                                        <span
                                            class="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75"
                                        ></span>
                                        <span
                                            class="relative inline-flex h-2 w-2 rounded-full bg-primary"
                                        ></span>
                                    </span>
                                    <span class="truncate text-sm font-medium text-on-surface">
                                        {run.task_name}{#if suffix}<span
                                                class="text-on-surface-muted">{suffix}</span
                                            >{/if}
                                    </span>
                                </div>
                                <p class="mt-1 text-xs text-on-surface-muted">
                                    {formatRunDurationLabel(run)} &middot; {formatTriggeredByLabel(
                                        run.triggered_by,
                                    )}
                                </p>
                            </div>
                            <ArrowRight
                                size={14}
                                class="shrink-0 text-on-surface-faint transition-colors group-hover:text-primary"
                            />
                        </div>
                    </TaskCard>
                {/each}
            </div>
        {/if}
    </Card>

    <!-- Up next -->
    {#if showUpcoming}
        <Card>
            <div class="flex items-center justify-between gap-3">
                <h3 class="text-sm font-semibold text-on-surface">Up next</h3>
                <Badge variant="info">{upcomingTasks.length}</Badge>
            </div>

            {#if upcomingTasks.length === 0}
                <div class="mt-4 flex items-center gap-2 rounded-lg bg-surface-sunken px-3 py-2.5">
                    <Clock3 size={14} class="text-on-surface-muted" />
                    <span class="text-sm text-on-surface-muted">No scheduled runs queued</span>
                </div>
            {:else}
                <div class="mt-4 space-y-2">
                    {#each upcomingTasks as task (task.task.id)}
                        <TaskCard accent="aurora" onclick={() => viewTask(task.task.name)}>
                            <div class="flex items-start justify-between gap-2">
                                <div class="min-w-0 flex-1">
                                    <div class="flex flex-wrap items-center gap-1.5">
                                        <span class="truncate text-sm font-medium text-on-surface">
                                            {task.task.name}
                                        </span>
                                        {#if task.task.group}
                                            <Badge variant="default" size="sm"
                                                >{task.task.group}</Badge
                                            >
                                        {/if}
                                        {#if task.task.compose}
                                            <ComposeBadge
                                                file={task.task.compose.file}
                                                service={task.task.compose.service}
                                                projectName={task.task.compose.project_name}
                                            />
                                        {/if}
                                        {#if task.task.source}
                                            <TaskSourceBadge
                                                name={task.task.name}
                                                source={task.task.source}
                                                sourceFile={task.task.source_file}
                                            />
                                        {/if}
                                    </div>
                                    <p class="mt-1 text-xs text-on-surface-muted">
                                        {formatTaskNextRunLabel(task, now)}
                                    </p>
                                </div>
                                <ArrowRight
                                    size={14}
                                    class="shrink-0 text-on-surface-faint transition-colors group-hover:text-info"
                                />
                            </div>

                            {#if task.task.cron}
                                <p class="mt-2 font-mono text-xs text-on-surface-muted">
                                    {task.task.cron}
                                </p>
                            {/if}
                        </TaskCard>
                    {/each}
                </div>
            {/if}
        </Card>
    {/if}
</div>
