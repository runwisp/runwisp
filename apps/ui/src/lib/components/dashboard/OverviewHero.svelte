<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

<script lang="ts">
    import { ArrowRight, CircleAlert, ShieldCheck, Sparkles, Zap } from "@lucide/svelte";
    import Badge from "@runwisp/ui/components/Badge.svelte";
    import Card from "@runwisp/ui/components/Card.svelte";
    import { formatBytes, Sparkline } from "@runwisp/ui";
    import { type Component } from "svelte";
    import type { MetricsSample } from "$lib/api";
    import { pluralize } from "./overview-format.js";
    import type { OverviewSummary } from "./overview.js";
    import type { DaemonState, DaemonStats } from "@runwisp/ui";

    type BadgeTone = "default" | "primary" | "success" | "warning" | "danger" | "info";

    interface SummaryCard {
        label: string;
        value: string;
        detail: string;
        icon: Component;
        accentClass: string;
        iconWrapClass: string;
        iconClass: string;
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
        onViewAllRuns,
    } = $props<{
        daemonState: DaemonState;
        stats: DaemonStats;
        summary: OverviewSummary;
        systemHealth: SystemHealth;
        completedRunsCount: number;
        healthyTasksCount: number;
        metricsHistory?: MetricsSample[];
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

    function createHealthyTasksCard(
        currentSummary: OverviewSummary,
        currentHealthyTasksCount: number,
    ): SummaryCard {
        const isFullyHealthy =
            currentSummary.totalTasks > 0 && currentHealthyTasksCount === currentSummary.totalTasks;

        return {
            label: "Healthy tasks",
            value: `${currentHealthyTasksCount}/${currentSummary.totalTasks}`,
            detail:
                currentSummary.totalTasks > 0
                    ? `${currentHealthyTasksCount} task${pluralize(currentHealthyTasksCount)} without active failures`
                    : "No tasks loaded yet",
            icon: ShieldCheck,
            accentClass: isFullyHealthy
                ? "border-success-200 bg-success-50/80"
                : "border-outline bg-surface-raised/80",
            iconWrapClass: isFullyHealthy ? "bg-success-100" : "bg-mist-100",
            iconClass: isFullyHealthy ? "text-success-700" : "text-mist-500",
        };
    }

    function createAttentionCard(attentionTasksCount: number): SummaryCard {
        const hasAttentionTasks = attentionTasksCount > 0;

        return {
            label: "Needs attention",
            value: String(attentionTasksCount),
            detail: hasAttentionTasks
                ? "Failures, crashes, stops, and timeouts"
                : "No incidents waiting",
            icon: CircleAlert,
            accentClass: hasAttentionTasks
                ? "border-danger-200 bg-danger-50/80"
                : "border-outline bg-surface-raised/80",
            iconWrapClass: hasAttentionTasks ? "bg-danger-100" : "bg-mist-100",
            iconClass: hasAttentionTasks ? "text-danger-700" : "text-mist-500",
        };
    }

    function createRunningCard(activeRunsCount: number): SummaryCard {
        const hasRunningTasks = activeRunsCount > 0;

        return {
            label: "Running now",
            value: String(activeRunsCount),
            detail: hasRunningTasks
                ? `${activeRunsCount} live execution${pluralize(activeRunsCount)}`
                : "Nothing executing",
            icon: Zap,
            accentClass: hasRunningTasks
                ? "border-wisp-200 bg-wisp-50/80"
                : "border-outline bg-surface-raised/80",
            iconWrapClass: hasRunningTasks ? "bg-wisp-100" : "bg-mist-100",
            iconClass: hasRunningTasks ? "text-wisp-700" : "text-mist-500",
        };
    }

    function createRecentSuccessCard(
        successRate: number,
        currentCompletedRunsCount: number,
    ): SummaryCard {
        const hasCompletedRuns = currentCompletedRunsCount > 0;
        const isPerfectSuccessRate = hasCompletedRuns && successRate >= 100;

        return {
            label: "Recent success",
            value: hasCompletedRuns ? `${successRate}%` : "-",
            detail: hasCompletedRuns
                ? `Across ${currentCompletedRunsCount} completed run${pluralize(currentCompletedRunsCount)}`
                : "Waiting for first completed run",
            icon: Sparkles,
            accentClass: isPerfectSuccessRate
                ? "border-success-200 bg-success-50/80"
                : hasCompletedRuns
                  ? "border-warning-200 bg-warning-50/80"
                  : "border-outline bg-surface-raised/80",
            iconWrapClass: isPerfectSuccessRate
                ? "bg-success-100"
                : hasCompletedRuns
                  ? "bg-warning-100"
                  : "bg-mist-100",
            iconClass: isPerfectSuccessRate
                ? "text-success-700"
                : hasCompletedRuns
                  ? "text-warning-700"
                  : "text-mist-500",
        };
    }

    function createDaemonFacts(currentDaemonState: DaemonState): DaemonFact[] {
        return [
            {
                label: "RunWisp",
                value: `${currentDaemonState.name} v${currentDaemonState.version}`,
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
            cpu: sample.cpu,
            mem: sample.mem,
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
    <div class="space-y-5">
        <!-- Header bar -->
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
                <span class="text-sm text-mist-500">
                    {summary.totalTasks} task{pluralize(summary.totalTasks)}
                </span>
            </div>

            <button
                class="inline-flex items-center gap-1.5 text-sm font-medium text-mist-600 transition-colors hover:text-mist-950"
                onclick={() => onViewAllRuns?.()}
            >
                View all runs
                <ArrowRight size={14} />
            </button>
        </Card>

        <!-- Stat cards -->
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            {#each summaryCards as card (card.label)}
                {@const Icon = card.icon}
                <div class="rounded-xl border p-4 {card.accentClass}">
                    <div class="flex items-start justify-between gap-3">
                        <div class="space-y-1">
                            <p
                                class="text-2xs font-semibold tracking-widest text-mist-500 uppercase"
                            >
                                {card.label}
                            </p>
                            <p class="text-2xl font-semibold tracking-tight text-mist-950">
                                {card.value}
                            </p>
                        </div>
                        <div
                            class="flex h-9 w-9 items-center justify-center rounded-lg {card.iconWrapClass}"
                        >
                            <Icon size={16} class={card.iconClass} />
                        </div>
                    </div>
                    <p class="mt-2 text-xs text-mist-500">{card.detail}</p>
                </div>
            {/each}
        </div>

        <!-- Runner facts -->
        <div
            class="flex flex-wrap items-center gap-x-6 gap-y-2 rounded-xl border border-mist-100 bg-mist-50/60 px-5 py-3 text-sm"
        >
            {#each daemonFacts as fact, i (fact.label)}
                {#if i > 0}
                    <span class="hidden text-mist-300 sm:inline">|</span>
                {/if}
                <span>
                    <span class="font-medium text-mist-500">{fact.label}</span>
                    <span class="ml-1.5 text-mist-950">{fact.value}</span>
                </span>
            {/each}
        </div>
    </div>

    <!-- System resources sidebar -->
    <Card padding="lg">
        <div class="flex items-center justify-between gap-3">
            <h2 class="text-sm font-semibold text-mist-950">System resources</h2>
            <Badge variant={stats.cpuUsage >= 85 || stats.memUsage >= 85 ? "warning" : "success"}>
                {stats.cpuUsage >= 85 || stats.memUsage >= 85 ? "High load" : "Steady"}
            </Badge>
        </div>

        <div class="mt-4 space-y-4">
            <div>
                <div class="mb-1.5 flex items-center justify-between text-sm">
                    <span class="text-mist-500">CPU</span>
                    <span class="font-semibold text-mist-950">{formatUsage(stats.cpuUsage)}</span>
                </div>
                <div class="overflow-hidden rounded-lg border border-mist-100 bg-mist-50/50">
                    <Sparkline data={cpuData} color="#1e293b" height={44} />
                </div>
            </div>

            <div>
                <div class="mb-1.5 flex items-baseline justify-between text-sm">
                    <span class="text-mist-500">Memory</span>
                    <span class="flex items-baseline gap-2">
                        {#if latestSample}
                            <span class="text-xs text-mist-400">
                                {formatBytes(latestSample.mem_used)} / {formatBytes(
                                    latestSample.mem_total,
                                )}
                            </span>
                        {/if}
                        <span class="font-semibold text-mist-950"
                            >{formatUsage(stats.memUsage)}</span
                        >
                    </span>
                </div>
                <div class="overflow-hidden rounded-lg border border-mist-100 bg-mist-50/50">
                    <Sparkline data={memData} color="#0284c7" height={44} />
                </div>
            </div>
        </div>
    </Card>
</section>
