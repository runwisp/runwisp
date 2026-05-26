<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { Activity, ArrowRight, Clock3, ShieldAlert, ShieldCheck } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import ComposeBadge from "../ComposeBadge.svelte";
    import { getRunStatusConfig, TaskCard } from "@runwisp/ui";
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
        onTaskClick,
        onRunClick,
    } = $props<{
        attentionTasks?: TaskOverview[];
        runningNow?: Run[];
        upcomingTasks?: TaskOverview[];
        onTaskClick?: (taskName: string) => void;
        onRunClick?: (taskName: string, runId: string) => void;
    }>();

    function viewTask(taskName: string): void {
        onTaskClick?.(taskName);
    }

    function viewRun(run: Run): void {
        onRunClick?.(run.task_name, run.id);
    }
</script>

<div class="grid gap-4 lg:grid-cols-3">
    <!-- Needs attention -->
    <Card>
        <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-mist-950">Needs attention</h3>
            <Badge variant={attentionTasks.length > 0 ? "danger" : "success"}>
                {attentionTasks.length}
            </Badge>
        </div>

        {#if attentionTasks.length === 0}
            <div class="mt-4 flex items-center gap-2 rounded-lg bg-success-50 px-3 py-2.5">
                <ShieldCheck size={14} class="text-success-700" />
                <span class="text-sm text-success-700">Nothing waiting for triage</span>
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
                                    <span class="truncate text-sm font-medium text-mist-950">
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
                                <p class="mt-1 text-xs text-mist-500">
                                    Last run {formatTaskLastRunLabel(task)}
                                </p>
                            </div>
                            <ShieldAlert size={14} class="shrink-0 text-danger-400" />
                        </div>

                        <div
                            class="mt-2 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-mist-500"
                        >
                            <span>Next {formatTaskNextRunLabel(task)}</span>
                            {#if isFailureEndReason(task.lastRun?.end_reason)}
                                <span class="text-danger-600">
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
            <h3 class="text-sm font-semibold text-mist-950">Running now</h3>
            <Badge variant={runningNow.length > 0 ? "primary" : "default"}>
                {runningNow.length}
            </Badge>
        </div>

        {#if runningNow.length === 0}
            <div class="mt-4 flex items-center gap-2 rounded-lg bg-mist-50 px-3 py-2.5">
                <Activity size={14} class="text-mist-500" />
                <span class="text-sm text-mist-500">Nothing is running</span>
            </div>
        {:else}
            <div class="mt-4 space-y-2">
                {#each runningNow as run (run.id)}
                    <TaskCard accent="wisp" onclick={() => viewRun(run)}>
                        <div class="flex items-start justify-between gap-2">
                            <div class="min-w-0 flex-1">
                                <div class="flex items-center gap-2">
                                    <span class="relative flex h-2 w-2 shrink-0">
                                        <span
                                            class="absolute inline-flex h-full w-full animate-ping rounded-full bg-wisp-400 opacity-75"
                                        ></span>
                                        <span
                                            class="relative inline-flex h-2 w-2 rounded-full bg-wisp-500"
                                        ></span>
                                    </span>
                                    <span class="truncate text-sm font-medium text-mist-950">
                                        {run.task_name}{#if run.instance_index > 0}<span
                                                class="text-mist-500">#{run.instance_index}</span
                                            >{/if}
                                    </span>
                                </div>
                                <p class="mt-1 text-xs text-mist-500">
                                    {formatRunDurationLabel(run)} &middot; {formatTriggeredByLabel(
                                        run.triggered_by,
                                    )}
                                </p>
                            </div>
                            <ArrowRight
                                size={14}
                                class="shrink-0 text-mist-300 transition-colors group-hover:text-wisp-600"
                            />
                        </div>
                    </TaskCard>
                {/each}
            </div>
        {/if}
    </Card>

    <!-- Up next -->
    <Card>
        <div class="flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-mist-950">Up next</h3>
            <Badge variant="info">{upcomingTasks.length}</Badge>
        </div>

        {#if upcomingTasks.length === 0}
            <div class="mt-4 flex items-center gap-2 rounded-lg bg-mist-50 px-3 py-2.5">
                <Clock3 size={14} class="text-mist-500" />
                <span class="text-sm text-mist-500">No scheduled runs queued</span>
            </div>
        {:else}
            <div class="mt-4 space-y-2">
                {#each upcomingTasks as task (task.task.id)}
                    <TaskCard accent="aurora" onclick={() => viewTask(task.task.name)}>
                        <div class="flex items-start justify-between gap-2">
                            <div class="min-w-0 flex-1">
                                <div class="flex flex-wrap items-center gap-1.5">
                                    <span class="truncate text-sm font-medium text-mist-950">
                                        {task.task.name}
                                    </span>
                                    {#if task.task.group}
                                        <Badge variant="default" size="sm">{task.task.group}</Badge>
                                    {/if}
                                    {#if task.task.compose}
                                        <ComposeBadge
                                            file={task.task.compose.file}
                                            service={task.task.compose.service}
                                            projectName={task.task.compose.project_name}
                                        />
                                    {/if}
                                </div>
                                <p class="mt-1 text-xs text-mist-500">
                                    {formatTaskNextRunLabel(task)}
                                </p>
                            </div>
                            <ArrowRight
                                size={14}
                                class="shrink-0 text-mist-300 transition-colors group-hover:text-aurora-600"
                            />
                        </div>

                        {#if task.task.cron}
                            <p class="mt-2 font-mono text-xs text-mist-500">
                                {task.task.cron}
                            </p>
                        {/if}
                    </TaskCard>
                {/each}
            </div>
        {/if}
    </Card>
</div>
