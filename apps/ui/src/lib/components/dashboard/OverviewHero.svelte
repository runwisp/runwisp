<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { ArrowRight, Cloud } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import { formatBytes, Sparkline } from "@runwisp/ui";
    import type { MetricsSample } from "$lib/api";
    import { pluralize } from "./overview-format.js";
    import type { OverviewSummary } from "./overview.js";
    import type { DaemonState, DaemonStats } from "@runwisp/ui";

    type BadgeTone = "default" | "primary" | "success" | "warning" | "danger" | "info";

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

    interface SystemHealth {
        label: string;
        variant: BadgeTone;
        detail: string;
    }

    interface ResourcePoint {
        cpu: number;
        mem: number;
    }

    interface DaemonFact {
        label: string;
        value: string;
    }

    const CHART_POINTS = 32;

    let {
        daemonState,
        stats,
        summary,
        systemHealth,
        completedRunsCount,
        healthyTasksCount,
        metricsHistory = [],
        cloudMode = false,
        onViewAllRuns,
    } = $props<{
        daemonState: DaemonState;
        stats: DaemonStats;
        summary: OverviewSummary;
        systemHealth: SystemHealth;
        completedRunsCount: number;
        healthyTasksCount: number;
        metricsHistory?: MetricsSample[];
        cloudMode?: boolean;
        onViewAllRuns?: () => void;
    }>();

    let resourcePoints = $derived(metricsHistory.map(toResourcePoint));
    let latestSample = $derived(metricsHistory[metricsHistory.length - 1]);
    let cpuData = $derived(
        fitToLength(
            resourcePoints.map((point: ResourcePoint) => point.cpu),
            CHART_POINTS,
        ),
    );
    let memData = $derived(
        fitToLength(
            resourcePoints.map((point: ResourcePoint) => point.mem),
            CHART_POINTS,
        ),
    );
    let summaryCards = $derived(
        createSummaryCards(summary, stats, completedRunsCount, healthyTasksCount),
    );
    let daemonFacts = $derived(createDaemonFacts(daemonState));

    function createSummaryCards(
        currentSummary: OverviewSummary,
        currentStats: DaemonStats,
        currentCompletedRunsCount: number,
        currentHealthyTasksCount: number,
    ): SummaryCard[] {
        return [
            createHealthyTasksCard(currentSummary, currentHealthyTasksCount),
            createAttentionCard(currentSummary.attentionTasks),
            createRunningCard(currentSummary.activeRuns),
            createRecentSuccessCard(currentStats.successRate, currentCompletedRunsCount),
        ];
    }

    const CALM_PANE = "border-outline bg-surface-raised";
    const ALARM_PANE = "border-danger-soft-border bg-danger-soft/60";

    const NEUTRAL_VALUE = "text-on-surface";
    const IDLE_VALUE = "text-on-surface-faint";
    const WARNING_VALUE = "text-warning-soft-text";
    const DANGER_VALUE = "text-danger-soft-text";
    const LIVE_VALUE = "text-primary";

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

    function createAttentionCard(attentionTasksCount: number): SummaryCard {
        const hasAttentionTasks = attentionTasksCount > 0;

        return {
            label: "needs attention",
            value: String(attentionTasksCount),
            detail: hasAttentionTasks
                ? "Failures, crashes, stops, and timeouts"
                : "No incidents waiting",
            valueClass: hasAttentionTasks ? DANGER_VALUE : IDLE_VALUE,
            accentClass: hasAttentionTasks ? ALARM_PANE : CALM_PANE,
        };
    }

    function createRunningCard(activeRunsCount: number): SummaryCard {
        const hasRunningTasks = activeRunsCount > 0;

        return {
            label: "running now",
            value: String(activeRunsCount),
            detail: hasRunningTasks
                ? `${activeRunsCount} live execution${pluralize(activeRunsCount)}`
                : "Nothing executing",
            valueClass: hasRunningTasks ? LIVE_VALUE : IDLE_VALUE,
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

    function createDaemonFacts(currentDaemonState: DaemonState): DaemonFact[] {
        return [
            {
                label: "RunWisp",
                value: `v${currentDaemonState.version}`,
            },
            {
                label: "Host",
                value: `${currentDaemonState.host} (${currentDaemonState.cpus} cores)`,
            },
            {
                label: "Uptime",
                value: currentDaemonState.uptime,
            },
            {
                label: "Fingerprint",
                value: currentDaemonState.fingerprint,
            },
        ];
    }

    function toResourcePoint(sample: MetricsSample): ResourcePoint {
        return {
            cpu: sample.cpuUsage,
            mem: sample.memUsage,
        };
    }

    function fitToLength(values: number[], length: number): number[] {
        if (values.length > length) {
            return values.slice(values.length - length);
        }

        if (values.length < length) {
            return Array(length - values.length)
                .fill(0)
                .concat(values);
        }

        return values;
    }

    function formatUsage(value: number): string {
        return `${Math.round(value)}%`;
    }
</script>

<section class="grid gap-5 xl:grid-cols-[minmax(0,1fr)_320px]">
    <div class="flex flex-col gap-5">
        {#if cloudMode}
            <p class="flex items-center gap-1.5 text-xs text-on-surface-muted">
                <Cloud size={12} class="shrink-0 text-info" />
                Managed by RunWisp Cloud · scheduling handled in the cloud.
            </p>
        {/if}

        <Card
            padding="none"
            bodyClass="flex flex-wrap items-center justify-between gap-3 px-5 py-3.5"
        >
            <div class="flex flex-wrap items-center gap-2">
                <Badge
                    variant={daemonState.status === "connected" ? "success" : "danger"}
                    dot
                    class="px-3 py-1"
                >
                    {daemonState.status === "connected" ? "Online" : "Offline"}
                </Badge>
                <Badge variant={systemHealth.variant} class="px-3 py-1">
                    {systemHealth.label}
                </Badge>
                <span class="font-mono text-sm text-on-surface-muted tabular-nums">
                    {summary.totalTasks} task{pluralize(summary.totalTasks)}
                </span>
            </div>

            <button
                class="inline-flex items-center gap-1.5 font-mono text-sm font-medium text-on-surface-muted hover:text-primary"
                onclick={() => onViewAllRuns?.()}
            >
                View all runs
                <ArrowRight size={14} />
            </button>
        </Card>

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
                    <p
                        class="border-t border-outline-faint px-4 py-2.5 text-xs text-on-surface-muted"
                    >
                        {card.detail}
                    </p>
                </div>
            {/each}
        </div>

        <!-- Runner facts. Grows to eat the leftover height so its bottom edge
             lands on the resources card's. -->
        <div
            class="flex grow flex-wrap content-center items-center gap-x-4 gap-y-2 rounded-[4px] border border-outline-faint bg-surface-sunken/60 px-5 py-3 text-sm lg:gap-x-6"
        >
            {#each daemonFacts as fact, i (fact.label)}
                <!-- The separator travels inside its fact so a wrap never leaves
                     a dangling | at the end of a line. -->
                <span class="inline-flex items-center gap-x-4 lg:gap-x-6">
                    {#if i > 0}
                        <span class="hidden text-on-surface-faint sm:inline" aria-hidden="true"
                            >|</span
                        >
                    {/if}
                    <span>
                        <span
                            class="font-mono text-xs font-medium tracking-wide text-on-surface-muted"
                            >{fact.label}</span
                        >
                        <span class="ml-1.5 font-mono text-xs text-on-surface tabular-nums"
                            >{fact.value}</span
                        >
                    </span>
                </span>
            {/each}
        </div>
    </div>

    <Card padding="lg">
        <div class="flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-on-surface">System resources</h2>
            <Badge variant={stats.cpuUsage >= 85 || stats.memUsage >= 85 ? "warning" : "success"}>
                {stats.cpuUsage >= 85 || stats.memUsage >= 85 ? "High load" : "Steady"}
            </Badge>
        </div>

        <div class="mt-4 space-y-4">
            <div>
                <div class="mb-1.5 flex items-center justify-between text-sm">
                    <span class="font-mono text-xs tracking-wide text-on-surface-muted">CPU</span>
                    <span class="font-mono font-semibold text-on-surface tabular-nums"
                        >{formatUsage(stats.cpuUsage)}</span
                    >
                </div>
                <div
                    class="overflow-hidden rounded-[4px] border border-outline-faint bg-surface-sunken/50 text-primary"
                >
                    <Sparkline data={cpuData} height={44} />
                </div>
            </div>

            <div>
                <div class="mb-1.5 flex items-baseline justify-between text-sm">
                    <span class="font-mono text-xs tracking-wide text-on-surface-muted">Memory</span
                    >
                    <span class="flex items-baseline gap-2">
                        {#if latestSample}
                            <span class="font-mono text-xs text-on-surface-faint tabular-nums">
                                {formatBytes(latestSample.memUsed)} / {formatBytes(
                                    latestSample.memTotal,
                                )}
                            </span>
                        {/if}
                        <span class="font-mono font-semibold text-on-surface tabular-nums"
                            >{formatUsage(stats.memUsage)}</span
                        >
                    </span>
                </div>
                <div
                    class="overflow-hidden rounded-[4px] border border-outline-faint bg-surface-sunken/50 text-info"
                >
                    <Sparkline data={memData} height={44} />
                </div>
            </div>
        </div>
    </Card>
</section>
