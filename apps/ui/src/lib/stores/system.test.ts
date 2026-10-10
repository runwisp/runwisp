// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";

const { api, connection } = vi.hoisted(() => {
    const sample = { at: "2026-10-09T12:00:00Z", cpuUsage: 12, memUsage: 34 };
    const reconnect: (() => void)[] = [];
    return {
        api: {
            getStats: vi.fn(() =>
                Promise.resolve({ version: "1.6.0", uptime: "1m", cpuUsage: 1, memUsage: 2 }),
            ),
            getInfo: vi.fn(() =>
                Promise.resolve({
                    fingerprint: "f",
                    name: "",
                    resolvedTimezone: "UTC",
                    timezoneSource: "system",
                    configStale: false,
                    stationEnabled: false,
                    schedulingActive: true,
                    updateAvailable: false,
                    latestVersion: "",
                    checkUpdates: false,
                    startedAt: "2026-10-09T11:00:00Z",
                }),
            ),
            getMetricsHistory: vi.fn(() => Promise.resolve([sample])),
        },
        connection: {
            status: "disconnected",
            reconnect,
            onReconnect(listener: () => void) {
                reconnect.push(listener);
                return () => undefined;
            },
        },
    };
});

vi.mock("$lib/api", () => ({
    systemApi: api,
    systemEventSchema: {},
    configStaleEventSchema: {},
}));
vi.mock("$lib/stores/connection.svelte", () => ({ connectionStore: connection }));
vi.mock("$lib/stores/app-stream", () => ({
    appEventStream: { subscribe: () => () => undefined },
}));

const { systemStore } = await import("./system.svelte");

describe("systemStore", () => {
    it("backfills the metrics chart while the stream is down, and again on reconnect", async () => {
        await systemStore.init();
        expect(api.getMetricsHistory).toHaveBeenCalledTimes(1);
        expect(systemStore.metricsHistory).toHaveLength(1);

        expect(connection.reconnect).toHaveLength(1);
        for (const listener of connection.reconnect) listener();
        expect(api.getMetricsHistory).toHaveBeenCalledTimes(2);
    });
});
