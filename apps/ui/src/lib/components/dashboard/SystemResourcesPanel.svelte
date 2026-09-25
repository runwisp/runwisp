<!-- SPDX-FileCopyrightText: PoppyCake, s.r.o. -->
<!-- SPDX-License-Identifier: GPL-3.0-or-later -->

<script lang="ts">
    import { formatBytes, Sparkline, Badge, Card } from "@runwisp/ui";
    import type { MetricsSample } from "$lib/api";
    import type { DaemonStats } from "@runwisp/ui";

    interface ResourcePoint {
        cpu: number;
        mem: number;
    }

    const CHART_POINTS = 32;

    let { stats, metricsHistory = [] } = $props<{
        stats: DaemonStats;
        metricsHistory?: MetricsSample[];
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
                <span class="font-mono text-xs tracking-wide text-on-surface-muted">Memory</span>
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
