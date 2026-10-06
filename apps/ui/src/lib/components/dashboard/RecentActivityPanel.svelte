<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ArrowRight, RotateCcwClock } from "@lucide/svelte";
    import {
        arrival,
        leave,
        shift,
        RUN_STATUS_CONFIG,
        instanceSuffix,
        Card,
        EmptyState,
        formatRelativeTime,
        formatTriggeredByLabel,
    } from "@runwisp/ui";
    import type { RunMotion } from "@runwisp/ui";
    import { displayStatus, type Run } from "@runwisp/common";
    import { formatRunDurationLabel } from "./overview-format.js";

    let {
        recentActivity = [],
        now = new Date(),
        onRunClick,
        onViewAllRuns,
        getInstanceCount = () => 1,
        motion,
    }: {
        recentActivity?: Run[];
        now?: Date;
        onRunClick?: (taskName: string, runId: string) => void;
        onViewAllRuns?: () => void;
        getInstanceCount?: (taskName: string) => number;
        // Runs that finished live moments ago drop in with the shared arrival
        // cue. Rows only ever leave this list live (deleted, or pushed off the
        // bottom), so they always sweep out.
        motion?: RunMotion;
    } = $props();

    function viewRun(run: Run): void {
        onRunClick?.(run.taskName, run.id);
    }
</script>

<Card padding="lg">
    <div class="flex items-center justify-between gap-3">
        <h2 class="text-sm font-semibold text-on-surface">Recent activity</h2>
        <button
            class="inline-flex items-center gap-1 font-mono text-xs font-medium text-on-surface-muted hover:text-primary"
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
                icon={RotateCcwClock}
            />
        </div>
    {:else}
        <div class="mt-4 space-y-1.5">
            {#each recentActivity as run (run.id)}
                {@const status = displayStatus(run.status, run.endReason)}
                {@const statusConfig = RUN_STATUS_CONFIG[status]}
                {@const StatusIcon = statusConfig.icon}
                {@const suffix = instanceSuffix(run.instanceIndex, getInstanceCount(run.taskName))}

                <button
                    data-run-id={run.id}
                    animate:shift
                    use:arrival={motion?.arrived(run.id) ?? false}
                    out:leave={() => true}
                    class="group flex w-full items-start gap-3 rounded-[3px] p-2.5 text-left hover:bg-surface-sunken"
                    onclick={() => viewRun(run)}
                >
                    <div
                        class="flex h-8 w-8 shrink-0 items-center justify-center rounded-[3px] {statusConfig.bg}"
                    >
                        <StatusIcon
                            size={14}
                            class="{statusConfig.color} {status === 'running'
                                ? 'animate-spin'
                                : ''}"
                        />
                    </div>

                    <div class="min-w-0 flex-1">
                        <div class="flex items-center justify-between gap-2">
                            <div class="flex min-w-0 items-center gap-1.5">
                                <span
                                    class="truncate font-mono text-sm font-medium text-on-surface"
                                >
                                    {run.taskName}{#if suffix}<span class="text-on-surface-muted"
                                            >{suffix}</span
                                        >{/if}
                                </span>
                                <span
                                    class="shrink-0 rounded-[3px] px-1.5 py-0.5 font-mono text-2xs font-semibold uppercase {statusConfig.badge}"
                                >
                                    {status}
                                </span>
                            </div>
                            <ArrowRight
                                size={12}
                                class="shrink-0 text-on-surface-faint group-hover:text-on-surface-muted"
                            />
                        </div>

                        <p class="mt-0.5 font-mono text-xs text-on-surface-muted tabular-nums">
                            {formatRelativeTime(run.startedAt ?? run.createdAt, now)} &middot;
                            {formatRunDurationLabel(run)}
                            &middot; {formatTriggeredByLabel(run.triggeredBy)}
                            {#if run.isFailure}
                                <span class="text-danger-soft-text">· Exit {run.exitCode}</span>
                            {/if}
                        </p>
                    </div>
                </button>
            {/each}
        </div>
    {/if}
</Card>
