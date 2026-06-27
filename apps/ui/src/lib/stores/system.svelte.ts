// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { untrack } from "svelte";
import { systemApi, AuthRequiredError, systemEventSchema, configStaleEventSchema } from "$lib/api";
import { connectionStore } from "$lib/stores/connection.svelte";
import { appEventStream } from "$lib/stores/app-stream.svelte";
import { createLogger } from "$lib/utils/logger";

function createSystemStore() {
    const logger = createLogger("SystemStore");
    let name = $state("runwisp");
    let version = $state("—");
    let uptime = $state("—");
    let host = $state("unknown");
    let cpus = $state(0);
    let memTotal = $state(0);
    let cpuUsage = $state(0);
    let memUsage = $state(0);
    let os = $state("—");
    let arch = $state("—");
    let workDir = $state("—");
    let fingerprint = $state("—");
    let timezone = $state("");
    let timezoneSource = $state("");
    let configStale = $state(false);
    // Standalone assumptions until the first /api/info lands: a standalone
    // daemon must never flash a cloud chip or hide its scheduling UI during
    // hydration. Components read these getters reactively and self-correct.
    let cloudEnabled = $state(false);
    let schedulingActive = $state(true);

    let subscribed = false;
    let unsubscribes: (() => void)[] = [];

    // init seeds the store once from REST, then rides the shared app-event
    // stream for live updates — replacing the old 2s/10s polling of
    // /api/system + /api/info. Idempotent: the stream subscription is bound
    // once and survives re-entrant init() calls on auth.
    async function init() {
        await seed();
        subscribe();
    }

    // seed pulls the one-shot snapshot: static identity (host, os, fingerprint,
    // timezone, cloud/scheduling mode) that never changes for the daemon's
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
            name = sys.name;
            version = sys.version;
            uptime = sys.uptime;
            host = sys.host;
            cpus = sys.cpu_cores;
            memTotal = sys.mem_total;
            cpuUsage = sys.cpu_usage;
            memUsage = sys.mem_usage;
            os = sys.os;
            arch = sys.arch;
            workDir = sys.work_dir;
            fingerprint = info.fingerprint;
            timezone = info.resolved_timezone;
            timezoneSource = info.timezone_source;
            configStale = info.config_stale;
            cloudEnabled = info.cloud_enabled;
            schedulingActive = info.scheduling_active;
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
                try {
                    const parsed = systemEventSchema.parse(JSON.parse(data));
                    cpuUsage = parsed.sample.cpu;
                    memUsage = parsed.sample.mem;
                    memTotal = parsed.sample.mem_total;
                    uptime = parsed.uptime;
                } catch (e) {
                    logger.warn("Invalid system SSE payload", e);
                }
            }),
            appEventStream.subscribe("config.stale", (data) => {
                try {
                    configStale = configStaleEventSchema.parse(JSON.parse(data)).stale;
                } catch (e) {
                    logger.warn("Invalid config.stale SSE payload", e);
                }
            }),
        );
    }

    function disconnect() {
        for (const off of unsubscribes) off();
        unsubscribes = [];
        subscribed = false;
    }

    return {
        get name() {
            return name;
        },
        get version() {
            return version;
        },
        get uptime() {
            return uptime;
        },
        get host() {
            return host;
        },
        get cpus() {
            return cpus;
        },
        get memTotal() {
            return memTotal;
        },
        get cpuUsage() {
            return cpuUsage;
        },
        get memUsage() {
            return memUsage;
        },
        get os() {
            return os;
        },
        get arch() {
            return arch;
        },
        get workDir() {
            return workDir;
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
        get cloudEnabled() {
            return cloudEnabled;
        },
        get schedulingActive() {
            return schedulingActive;
        },
        init,
        disconnect,
    };
}

export const systemStore = createSystemStore();
