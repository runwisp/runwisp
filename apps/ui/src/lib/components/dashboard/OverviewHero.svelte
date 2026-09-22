<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { RadioTower } from "@lucide/svelte";
    import { formatCompactCount, pluralize } from "./overview-format.js";
    import type { OverviewSummary } from "./overview.js";
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
        accentClass: string;
    }

    let {
        stats,
        summary,
        totalRuns,
        completedRunsCount,
        healthyTasksCount,
        uptime,
        stationMode = false,
    } = $props<{
        stats: DaemonStats;
        summary: OverviewSummary;
        totalRuns: number;
        completedRunsCount: number;
        healthyTasksCount: number;
        uptime: string;
        stationMode?: boolean;
    }>();

    let summaryCards = $derived(
        createSummaryCards(
            summary,
            stats,
            totalRuns,
            completedRunsCount,
            healthyTasksCount,
            uptime,
        ),
    );

    function createSummaryCards(
        currentSummary: OverviewSummary,
        currentStats: DaemonStats,
        currentTotalRuns: number,
        currentCompletedRunsCount: number,
        currentHealthyTasksCount: number,
        uptimeLabel: string,
    ): SummaryCard[] {
        return [
            createHealthyTasksCard(currentSummary, currentHealthyTasksCount),
            createUptimeCard(uptimeLabel),
            createTotalRunsCard(currentTotalRuns),
            createRecentSuccessCard(currentStats.successRate, currentCompletedRunsCount),
        ];
    }

    const CALM_PANE = "border-outline bg-surface-raised";

    const NEUTRAL_VALUE = "text-on-surface";
    const IDLE_VALUE = "text-on-surface-faint";
    const WARNING_VALUE = "text-warning-soft-text";

    function createHealthyTasksCard(
        currentSummary: OverviewSummary,
        currentHealthyTasksCount: number,
    ): SummaryCard {
        const hasTasks = currentSummary.totalTasks > 0;
        const isFullyHealthy = hasTasks && currentHealthyTasksCount === currentSummary.totalTasks;

        return {
            label: "healthy tasks",
            value: `${currentHealthyTasksCount}/${currentSummary.totalTasks}`,
            detail: hasTasks
                ? `${currentHealthyTasksCount} task${pluralize(currentHealthyTasksCount)} without active failures`
                : "No tasks loaded yet",
            valueClass: !hasTasks ? IDLE_VALUE : isFullyHealthy ? NEUTRAL_VALUE : WARNING_VALUE,
            accentClass: CALM_PANE,
        };
    }

    function createUptimeCard(uptime: string): SummaryCard {
        return {
            label: "uptime",
            value: uptime,
            detail: "Since the daemon last started",
            valueClass: NEUTRAL_VALUE,
            accentClass: CALM_PANE,
        };
    }

    function createTotalRunsCard(currentTotalRuns: number): SummaryCard {
        const hasRuns = currentTotalRuns > 0;

        return {
            label: "total runs",
            value: formatCompactCount(currentTotalRuns),
            detail: hasRuns ? "Runs recorded since first launch" : "No runs recorded yet",
            valueClass: hasRuns ? NEUTRAL_VALUE : IDLE_VALUE,
            accentClass: CALM_PANE,
        };
    }

    function createRecentSuccessCard(
        successRate: number,
        currentCompletedRunsCount: number,
    ): SummaryCard {
        const hasCompletedRuns = currentCompletedRunsCount > 0;
        const isPerfectSuccessRate = hasCompletedRuns && successRate >= 100;

        return {
            label: "recent success",
            value: hasCompletedRuns ? `${successRate}%` : "—",
            detail: hasCompletedRuns
                ? `Across ${currentCompletedRunsCount} completed run${pluralize(currentCompletedRunsCount)}`
                : "Waiting for first completed run",
            valueClass: !hasCompletedRuns
                ? IDLE_VALUE
                : isPerfectSuccessRate
                  ? NEUTRAL_VALUE
                  : WARNING_VALUE,
            accentClass: CALM_PANE,
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
            <div class="relative rounded-[4px] border shadow-sm {card.accentClass}">
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
