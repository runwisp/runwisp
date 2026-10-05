<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { RadioTower } from "@lucide/svelte";
    import { formatCompactCount, pluralize } from "./overview-format.js";
    import type { DaemonStats } from "@runwisp/ui";

    // A stat pane, in the website's tmux-pane language: the label rides the top
    // hairline as a lowercase tab. State is carried by the number itself — it
    // stays neutral while things are fine and takes on a tone when they aren't,
    // so a bad pane is the single lit thing on the page instead of a second
    // label in the opposite corner arguing with the first.
    interface SummaryCard {
        label: string;
        value: string;
        detail: string;
        valueClass: string;
    }

    let {
        stats,
        totalTasks,
        totalRuns,
        completedRunsCount,
        healthyTasksCount,
        uptime,
        stationMode = false,
    } = $props<{
        stats: DaemonStats;
        totalTasks: number;
        totalRuns: number;
        completedRunsCount: number;
        healthyTasksCount: number;
        uptime: string;
        stationMode?: boolean;
    }>();

    const NEUTRAL_VALUE = "text-on-surface";
    const IDLE_VALUE = "text-on-surface-faint";
    const WARNING_VALUE = "text-warning-soft-text";

    let summaryCards = $derived<SummaryCard[]>([
        healthyTasksCard(),
        {
            label: "uptime",
            value: uptime,
            detail: "Since the daemon last started",
            valueClass: NEUTRAL_VALUE,
        },
        totalRunsCard(),
        recentSuccessCard(),
    ]);

    function healthyTasksCard(): SummaryCard {
        const hasTasks = totalTasks > 0;
        const isFullyHealthy = hasTasks && healthyTasksCount === totalTasks;

        return {
            label: "healthy tasks",
            value: `${healthyTasksCount}/${totalTasks}`,
            detail: hasTasks
                ? `${healthyTasksCount} task${pluralize(healthyTasksCount)} without active failures`
                : "No tasks loaded yet",
            valueClass: !hasTasks ? IDLE_VALUE : isFullyHealthy ? NEUTRAL_VALUE : WARNING_VALUE,
        };
    }

    function totalRunsCard(): SummaryCard {
        const hasRuns = totalRuns > 0;

        return {
            label: "total runs",
            value: formatCompactCount(totalRuns),
            detail: hasRuns ? "Runs recorded since first launch" : "No runs recorded yet",
            valueClass: hasRuns ? NEUTRAL_VALUE : IDLE_VALUE,
        };
    }

    function recentSuccessCard(): SummaryCard {
        const { successRate } = stats;
        const hasCompletedRuns = completedRunsCount > 0;
        const isPerfectSuccessRate = hasCompletedRuns && successRate >= 100;

        return {
            label: "recent success",
            value: hasCompletedRuns ? `${successRate}%` : "—",
            detail: hasCompletedRuns
                ? `Across ${completedRunsCount} completed run${pluralize(completedRunsCount)}`
                : "Waiting for first completed run",
            valueClass: !hasCompletedRuns
                ? IDLE_VALUE
                : isPerfectSuccessRate
                  ? NEUTRAL_VALUE
                  : WARNING_VALUE,
        };
    }
</script>

<div class="flex flex-col gap-5">
    {#if stationMode}
        <p class="flex items-center gap-1.5 text-xs text-on-surface-muted">
            <RadioTower size={12} class="shrink-0 text-info" />
            Managed by RunWisp Station · scheduling handled in the station.
        </p>
    {/if}

    <!-- Stat panes. The label sits ON the top hairline as a tmux pane title;
         the number owns the body and carries the state in its tone; the
         sentence is the pane foot. -->
    <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {#each summaryCards as card (card.label)}
            <div class="relative rounded-[4px] border border-outline bg-surface-raised shadow-sm">
                <span
                    class="absolute top-0 left-3.5 max-w-[calc(100%-2rem)] -translate-y-1/2 truncate bg-surface-sunken px-2 font-mono text-[10.5px] leading-[1.6] font-medium tracking-[0.06em] text-on-surface-muted"
                >
                    {card.label}
                </span>
                <p
                    class="px-4 pt-5 pb-4 font-mono text-[28px] leading-none font-extrabold tracking-[-0.02em] tabular-nums {card.valueClass}"
                >
                    {card.value}
                </p>
                <p class="border-t border-outline-faint px-4 py-2.5 text-xs text-on-surface-muted">
                    {card.detail}
                </p>
            </div>
        {/each}
    </div>
</div>
