// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { untrack } from "svelte";
import { systemApi, AuthRequiredError, systemEventSchema, configStaleEventSchema } from "$lib/api";
import { connectionStore } from "$lib/stores/connection.svelte";
import { appEventStream } from "$lib/stores/app-stream";
import { createLogger } from "@runwisp/common";
import { safeParseJSON } from "$lib/utils/parse";

function createSystemStore() {
    const logger = createLogger("SystemStore");
    let version = $state("—");
    let uptime = $state("—");
    let cpuUsage = $state(0);
    let memUsage = $state(0);
    let fingerprint = $state("—");
    let timezone = $state("");
    let timezoneSource = $state("");
    let configStale = $state(false);
    // Standalone assumptions until the first /api/daemon lands: a standalone
    // daemon must never flash a station chip or hide its scheduling UI during
    // hydration. Components read these getters reactively and self-correct.
    let stationEnabled = $state(false);
    let schedulingActive = $state(true);
    // Update availability lands from /api/daemon once the background checker has
    // heard back from concierge; false/empty until then and when the check is off.
    let updateAvailable = $state(false);
    let latestVersion = $state("");
    // Gate the feedback card: check_updates off keeps the dashboard from talking
    // to concierge at all; startedAt (epoch ms) drives its one-hour uptime wait.
    let checkUpdates = $state(false);
    let startedAt = $state(0);

    let subscribed = false;
    let unsubscribes: (() => void)[] = [];

    // init seeds the store once from REST, then rides the shared app-event
    // stream for live updates instead of polling /api/system + /api/daemon.
    // Idempotent: the stream subscription is bound once and survives
    // re-entrant init() calls on auth.
    async function init() {
        await seed();
        subscribe();
    }

    // seed pulls the one-shot snapshot: static identity (fingerprint,
    // timezone, station/scheduling mode) that never changes for the daemon's
    // lifetime, plus the initial cpu/mem/uptime so gauges aren't blank before
    // the first pushed sample lands.
    async function seed() {
        // init() is invoked from a reactive $effect (the layout's auth effect).
        // Read status untracked so seeding doesn't make that effect depend on
        // connectionStore.status, which oscillates (connecting↔connected↔
        // disconnected) and would otherwise re-run init() in a runaway loop.
        if (untrack(() => connectionStore.status) === "disconnected") return;
        try {
            const [sys, info] = await Promise.all([systemApi.getStats(), systemApi.getInfo()]);
            version = sys.version;
            uptime = sys.uptime;
            cpuUsage = sys.cpuUsage;
            memUsage = sys.memUsage;
            fingerprint = info.fingerprint;
            timezone = info.resolvedTimezone;
            timezoneSource = info.timezoneSource;
            configStale = info.configStale;
            stationEnabled = info.stationEnabled;
            schedulingActive = info.schedulingActive;
            updateAvailable = info.updateAvailable;
            latestVersion = info.latestVersion;
            checkUpdates = info.checkUpdates;
            const started = Date.parse(info.startedAt);
            startedAt = Number.isNaN(started) ? 0 : started;
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            // silent — system stats are secondary
        }
    }

    function subscribe() {
        if (subscribed) return;
        subscribed = true;
        unsubscribes.push(
            appEventStream.subscribe("system", (data) => {
                const parsed = safeParseJSON(data, systemEventSchema);
                if (!parsed.success) {
                    logger.warn("Invalid system SSE payload", parsed.error);
                    return;
                }
                cpuUsage = parsed.data.sample.cpuUsage;
                memUsage = parsed.data.sample.memUsage;
                uptime = parsed.data.uptime;
            }),
            appEventStream.subscribe("config.stale", (data) => {
                const parsed = safeParseJSON(data, configStaleEventSchema);
                if (!parsed.success) {
                    logger.warn("Invalid config.stale SSE payload", parsed.error);
                    return;
                }
                configStale = parsed.data.stale;
            }),
        );
    }

    function disconnect() {
        for (const off of unsubscribes) off();
        unsubscribes = [];
        subscribed = false;
    }

    return {
        get version() {
            return version;
        },
        get uptime() {
            return uptime;
        },
        get cpuUsage() {
            return cpuUsage;
        },
        get memUsage() {
            return memUsage;
        },
        get fingerprint() {
            return fingerprint;
        },
        get timezone() {
            return timezone;
        },
        get timezoneSource() {
            return timezoneSource;
        },
        get configStale() {
            return configStale;
        },
        get stationEnabled() {
            return stationEnabled;
        },
        get schedulingActive() {
            return schedulingActive;
        },
        get updateAvailable() {
            return updateAvailable;
        },
        get latestVersion() {
            return latestVersion;
        },
        get checkUpdates() {
            return checkUpdates;
        },
        get startedAt() {
            return startedAt;
        },
        init,
        disconnect,
    };
}

export const systemStore = createSystemStore();
