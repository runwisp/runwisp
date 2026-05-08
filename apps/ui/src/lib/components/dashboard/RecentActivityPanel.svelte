<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { ArrowRight, History } from "@lucide/svelte";
    import EmptyState from "@runwisp/ui/components/EmptyState.svelte";
    import { getRunStatusConfig, runDisplayStatus } from "@runwisp/ui";
    import { isFailureEndReason, type Run } from "@runwisp/common";
    import {
        formatRunDurationLabel,
        formatRunStartedLabel,
        formatTriggeredByLabel,
    } from "./overview-format.js";

    let {
        recentActivity = [],
        onRunClick,
        onViewAllRuns,
    } = $props<{
        recentActivity?: Run[];
        onRunClick?: (taskName: string, runId: string) => void;
        onViewAllRuns?: () => void;
    }>();

    function viewRun(run: Run): void {
        onRunClick?.(run.task_name, run.id);
    }
</script>

<div class="rounded-xl border border-mist-200 bg-white p-5 shadow-sm">
    <div class="flex items-center justify-between gap-3">
        <h2 class="text-sm font-semibold text-mist-950">Recent activity</h2>
        <button
            class="inline-flex items-center gap-1 text-xs font-medium text-mist-500 transition-colors hover:text-mist-950"
            onclick={() => onViewAllRuns?.()}
        >
            All runs
            <ArrowRight size={12} />
        </button>
    </div>

    {#if recentActivity.length === 0}
        <div class="mt-4">
            <EmptyState
                title="No recent activity"
                description="Runs will appear here once tasks begin executing."
                icon={History}
            />
        </div>
    {:else}
        <div class="mt-4 space-y-1.5">
            {#each recentActivity as run (run.id)}
                {@const status = runDisplayStatus(run)}
                {@const statusConfig = getRunStatusConfig(status)}
                {@const StatusIcon = statusConfig.icon}

                <button
                    class="group flex w-full items-start gap-3 rounded-lg p-2.5 text-left transition-colors hover:bg-mist-50"
                    onclick={() => viewRun(run)}
                >
                    <div
                        class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg {statusConfig.bg}"
                    >
                        <StatusIcon size={14} class={statusConfig.color} />
                    </div>

                    <div class="min-w-0 flex-1">
                        <div class="flex items-center justify-between gap-2">
                            <div class="flex min-w-0 items-center gap-1.5">
                                <span class="truncate text-sm font-medium text-mist-950">
                                    {run.task_name}{#if run.replica_index > 0}<span
                                            class="text-mist-500">#{run.replica_index}</span
                                        >{/if}
                                </span>
                                <span
                                    class="shrink-0 rounded-full px-1.5 py-0.5 text-[10px] font-semibold uppercase {statusConfig.badge}"
                                >
                                    {status}
                                </span>
                            </div>
                            <ArrowRight
                                size={12}
                                class="shrink-0 text-mist-300 transition-colors group-hover:text-mist-600"
                            />
                        </div>

                        <p class="mt-0.5 text-xs text-mist-500">
                            {formatRunStartedLabel(run)} &middot;
                            {formatRunDurationLabel(run)}
                            &middot; {formatTriggeredByLabel(run.triggered_by)}
                            {#if isFailureEndReason(run.end_reason)}
                                <span class="text-danger-600">· Exit {run.exit_code}</span>
                            {/if}
                        </p>
                    </div>
                </button>
            {/each}
        </div>
    {/if}
</div>
