// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import type { Run } from "@runwisp/common";
import { RunDeepLink } from "./run-deep-link.svelte";

function deferred() {
    let settle: { resolve: (r: Run) => void; reject: (e: Error) => void } = {
        resolve: () => {},
        reject: () => {},
    };
    const promise = new Promise<Run>((resolve, reject) => (settle = { resolve, reject }));
    return { promise, ...settle };
}

function makeRun(id: string): Run {
    return {
        id,
        taskName: "backup-db",
        createdAt: "2026-06-22T12:00:00.000Z",
        status: "ended",
        endReason: "succeeded",
        triggeredBy: "api",
        exitCode: 0,
        instanceIndex: 0,
        retryAttempt: 0,
        isFailure: false,
    };
}

const loaded = [makeRun("LOADED")];

describe("RunDeepLink", () => {
    it("ignores an older lookup that finishes after a newer link", async () => {
        const lookups = new Map([
            ["A", deferred()],
            ["B", deferred()],
        ]);
        const upsert = vi.fn();
        const link = new RunDeepLink(
            (id) => lookups.get(id)?.promise ?? Promise.reject(new Error("unexpected")),
            upsert,
        );

        link.resolve("A", loaded);
        link.resolve("B", loaded);
        lookups.get("A")?.reject(new Error("404"));
        await Promise.resolve();
        await Promise.resolve();

        expect(link.pending).toBe(true);
        expect(link.notFound).toBe(false);

        const b = makeRun("B");
        lookups.get("B")?.resolve(b);
        await Promise.resolve();
        await Promise.resolve();

        expect(link.pending).toBe(false);
        expect(link.notFound).toBe(false);
        expect(upsert).toHaveBeenCalledExactlyOnceWith(b);
    });

    it("latches not-found for a dead link and fetches it only once", async () => {
        const fetchRun = vi.fn(() => Promise.reject(new Error("404")));
        const link = new RunDeepLink(fetchRun, vi.fn());

        link.resolve("GONE", loaded);
        link.resolve("GONE", loaded); // a list update while the lookup is in flight
        await Promise.resolve();
        await Promise.resolve();

        expect(link.notFound).toBe(true);
        expect(link.pending).toBe(false);
        link.resolve("GONE", loaded);
        expect(fetchRun).toHaveBeenCalledOnce();
    });
});
