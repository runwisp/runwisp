// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { systemApi, AuthRequiredError } from "$lib/api";
import { connectionStore } from "$lib/stores/connection.svelte";

function createSystemStore() {
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

    async function refresh() {
        if (connectionStore.status === "disconnected") return;
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
        } catch (err) {
            if (err instanceof AuthRequiredError) return;
            // silent — system stats are secondary
        }
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
        refresh,
    };
}

export const systemStore = createSystemStore();
