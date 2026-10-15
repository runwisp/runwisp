// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { RUN_EVENT_TYPES, type RunUpdateEvent } from "$lib/types";

const { stream, connection } = vi.hoisted(() => {
    const listeners = new Map<string, (data: string) => void>();
    return {
        stream: {
            listeners,
            subscribe: vi.fn((type: string, listener: (data: string) => void) => {
                listeners.set(type, listener);
                return () => listeners.delete(type);
            }),
        },
        connection: { releaseSource: vi.fn() },
    };
});

vi.mock("./app-stream", () => ({ appEventStream: stream }));
vi.mock("./connection.svelte", () => ({
    connectionStore: connection,
    trackStreamHealth: () => () => undefined,
}));

const { runUpdatesStore } = await import("./run-updates");

function emit(type: string, data: string): void {
    const listener = stream.listeners.get(type);
    if (!listener) throw new Error(`nothing listens to ${type}`);
    listener(data);
}

describe("runUpdatesStore", () => {
    it("connects on the first handler and forwards only valid run events", () => {
        const got: RunUpdateEvent[] = [];
        const off = runUpdatesStore.subscribeToUpdates((event) => got.push(event));
        expect([...stream.listeners.keys()]).toEqual([...RUN_EVENT_TYPES]);

        emit("run.deleted", JSON.stringify({ runId: "r1", taskName: "backup" }));
        emit("run.deleted", "{not json");
        expect(got).toEqual([{ type: "run.deleted", data: { runId: "r1", taskName: "backup" } }]);

        off();
        emit("run.deleted", JSON.stringify({ runId: "r2", taskName: "backup" }));
        expect(got).toHaveLength(1);
    });

    it("drops every subscription and its health source on disconnect", () => {
        runUpdatesStore.disconnect();
        expect(stream.listeners.size).toBe(0);
        expect(connection.releaseSource).toHaveBeenCalledWith("run-updates");
    });
});
