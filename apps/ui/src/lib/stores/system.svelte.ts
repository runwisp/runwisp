// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { untrack } from "svelte";
import { systemApi, systemEventSchema, configStaleEventSchema, type MetricsSample } from "$lib/api";
import { connectionStore } from "$lib/stores/connection.svelte";
import { appEventStream } from "$lib/stores/app-stream";
import { createLogger } from "@runwisp/common";
import { safeParseJSON } from "$lib/utils/parse";
import type { ResourceUsage, Task } from "@runwisp/common";

// Live window for the metrics chart: seeded from /api/system/metrics, then
// grown by pushed samples (one every ~5s). 120 is about 10 minutes.
const METRICS_HISTORY_LIMIT = 120;

class SystemStore {
    version = $state("—");
    uptime = $state("—");
    cpuUsage = $state(0);
    memUsage = $state(0);
    metricsHistory = $state<MetricsSample[]>([]);
    fingerprint = $state("—");
    // [daemon] name; shown in place of the fingerprint when set.
    name = $state("");
    timezone = $state("");
    timezoneSource = $state("");
    configStale = $state(false);
    // Standalone assumptions until the first /api/daemon lands: a standalone
    // daemon must never flash a station chip or hide its scheduling UI during
    // hydration. Components read these reactively and self-correct.
    stationEnabled = $state(false);
    schedulingActive = $state(true);
    // Update availability lands from /api/daemon once the background checker has
    // heard back from concierge; false/empty until then and when the check is off.
    updateAvailable = $state(false);
    latestVersion = $state("");
    // Gate the feedback card: check_updates off keeps the dashboard from talking
    // to concierge at all; startedAt (epoch ms) drives its one-hour uptime wait.
    checkUpdates = $state(false);
    startedAt = $state(0);
    // Live CPU/memory per task from the system event. null until the first one
    // lands, so usageFor falls back to the usage the task was fetched with.
    #taskUsage = $state<Record<string, ResourceUsage> | null>(null);
    // Live CPU/memory per running run, by run ID, from the same event.
    #runUsage = $state<Record<string, ResourceUsage>>({});

    #subscribed = false;
    #unsubscribes: (() => void)[] = [];
    readonly #logger = createLogger("SystemStore");

    // Seeds the store once from REST, then rides the shared app-event stream
    // for live updates instead of polling /api/system + /api/daemon.
    // Idempotent: the stream subscription is bound once and survives
    // re-entrant init() calls on auth.
    async init(): Promise<void> {
        await this.#seed();
        this.#subscribe();
    }

    usageFor(task: Task): ResourceUsage | undefined {
        return this.#taskUsage ? this.#taskUsage[task.name] : task.usage;
    }

    // Browser tab title, prefixed with [daemon] name so tabs of several
    // daemons stay apart.
    title(page?: string): string {
        return [page, this.name, "RunWisp"].filter(Boolean).join(" · ");
    }

    runUsage(runId: string): ResourceUsage | undefined {
        return this.#runUsage[runId];
    }

    disconnect(): void {
        for (const off of this.#unsubscribes) off();
        this.#unsubscribes = [];
        this.#subscribed = false;
    }

    // Pulls the one-shot snapshot: static identity (fingerprint, timezone,
    // station/scheduling mode) that never changes for the daemon's lifetime,
    // plus the initial cpu/mem/uptime so gauges aren't blank before the first
    // pushed sample lands.
    async #seed(): Promise<void> {
        // init() is invoked from a reactive $effect (the layout's auth effect).
        // Read status untracked so seeding doesn't make that effect depend on
        // connectionStore.status, which oscillates (connecting↔connected↔
        // disconnected) and would otherwise re-run init() in a runaway loop.
        if (untrack(() => connectionStore.status) === "disconnected") return;
        try {
            const [sys, info, history] = await Promise.all([
                systemApi.getStats(),
                systemApi.getInfo(),
                // The chart is secondary: a failed backfill must not block the rest.
                systemApi.getMetricsHistory().catch(() => []),
            ]);
            this.version = sys.version;
            this.uptime = sys.uptime;
            this.cpuUsage = sys.cpuUsage;
            this.memUsage = sys.memUsage;
            this.metricsHistory = history;
            this.fingerprint = info.fingerprint;
            this.name = info.name;
            this.timezone = info.resolvedTimezone;
            this.timezoneSource = info.timezoneSource;
            this.configStale = info.configStale;
            this.stationEnabled = info.stationEnabled;
            this.schedulingActive = info.schedulingActive;
            this.updateAvailable = info.updateAvailable;
            this.latestVersion = info.latestVersion;
            this.checkUpdates = info.checkUpdates;
            const started = Date.parse(info.startedAt);
            this.startedAt = Number.isNaN(started) ? 0 : started;
        } catch {
            // silent, system stats are secondary
        }
    }

    #subscribe(): void {
        if (this.#subscribed) return;
        this.#subscribed = true;
        this.#unsubscribes.push(
            appEventStream.subscribe("system", (data) => {
                const parsed = safeParseJSON(data, systemEventSchema);
                if (!parsed.success) {
                    this.#logger.warn("Invalid system SSE payload", parsed.error);
                    return;
                }
                const { sample, uptime, tasks, runs } = parsed.data;
                this.cpuUsage = sample.cpuUsage;
                this.memUsage = sample.memUsage;
                this.metricsHistory = [...this.metricsHistory, sample].slice(
                    -METRICS_HISTORY_LIMIT,
                );
                this.uptime = uptime;
                this.#taskUsage = tasks ?? {};
                this.#runUsage = runs ?? {};
            }),
            appEventStream.subscribe("config.stale", (data) => {
                const parsed = safeParseJSON(data, configStaleEventSchema);
                if (!parsed.success) {
                    this.#logger.warn("Invalid config.stale SSE payload", parsed.error);
                    return;
                }
                this.configStale = parsed.data.stale;
            }),
        );
    }
}

export const systemStore = new SystemStore();
