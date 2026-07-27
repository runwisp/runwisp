// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";

// Stub $lib/api so importing AsyncData doesn't pull in the REST/SSE modules
// (and their zod schemas) — we only need AuthRequiredError's identity.
vi.mock("$lib/api", () => ({
    AuthRequiredError: class AuthRequiredError extends Error {},
}));

import { AsyncData } from "./async-data.svelte";

describe("AsyncData", () => {
    // Guards M9: a fetcher that ignores its abort signal can still resolve after
    // a newer fetch() superseded it. The success path must drop the stale
    // result instead of overwriting fresher data.
    it("does not let an aborted fetch overwrite fresher data", async () => {
        let resolveFirst: (v: string) => void = () => {};
        const first = new Promise<string>((r) => (resolveFirst = r));

        let call = 0;
        const ad = new AsyncData<string>(
            async () => {
                call += 1;
                // First call ignores the signal and resolves late.
                if (call === 1) return first;
                return "fresh";
            },
            { reloadOnReconnect: false, toastOnError: false },
        );

        const p1 = ad.fetch(); // starts the (pending) first fetch
        const p2 = ad.fetch(); // aborts the first, resolves with "fresh"
        await p2;
        expect(ad.data).toBe("fresh");

        // The stale first fetch resolves only now — it must not clobber "fresh".
        resolveFirst("stale");
        await p1;
        expect(ad.data).toBe("fresh");
    });
});
