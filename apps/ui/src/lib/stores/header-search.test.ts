// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, describe, expect, it } from "vitest";
import { headerSearchStore, type HeaderSearchSpec } from "./header-search.svelte";

function makeSpec(overrides: Partial<HeaderSearchSpec> = {}): HeaderSearchSpec {
    return { placeholder: "Search…", onSearch: () => {}, ...overrides };
}

// The store is a process-wide singleton; reset it between cases so state set by
// one test never leaks into the next.
afterEach(() => {
    headerSearchStore.unregister();
});

describe("headerSearchStore", () => {
    it("starts inactive with an empty query", () => {
        expect(headerSearchStore.active).toBe(false);
        expect(headerSearchStore.spec).toBeNull();
        expect(headerSearchStore.query).toBe("");
        expect(headerSearchStore.loading).toBe(false);
    });

    it("register activates the store and exposes the spec", () => {
        const spec = makeSpec({ placeholder: "Find runs" });
        headerSearchStore.register(spec);
        expect(headerSearchStore.active).toBe(true);
        expect(headerSearchStore.spec).toBe(spec);
        expect(headerSearchStore.spec?.placeholder).toBe("Find runs");
    });

    it("setQuery and setLoading update reactive state", () => {
        headerSearchStore.register(makeSpec());
        headerSearchStore.setQuery("abc");
        expect(headerSearchStore.query).toBe("abc");
        headerSearchStore.setLoading(true);
        expect(headerSearchStore.loading).toBe(true);
    });

    it("clear empties the query but leaves the spec registered", () => {
        headerSearchStore.register(makeSpec());
        headerSearchStore.setQuery("abc");
        headerSearchStore.clear();
        expect(headerSearchStore.query).toBe("");
        expect(headerSearchStore.active).toBe(true);
    });

    it("register resets a stale query and loading flag", () => {
        headerSearchStore.register(makeSpec());
        headerSearchStore.setQuery("stale");
        headerSearchStore.setLoading(true);
        headerSearchStore.register(makeSpec({ placeholder: "fresh" }));
        expect(headerSearchStore.query).toBe("");
        expect(headerSearchStore.loading).toBe(false);
        expect(headerSearchStore.spec?.placeholder).toBe("fresh");
    });

    it("unregister deactivates the store and clears all state", () => {
        headerSearchStore.register(makeSpec());
        headerSearchStore.setQuery("abc");
        headerSearchStore.setLoading(true);
        headerSearchStore.unregister();
        expect(headerSearchStore.active).toBe(false);
        expect(headerSearchStore.spec).toBeNull();
        expect(headerSearchStore.query).toBe("");
        expect(headerSearchStore.loading).toBe(false);
    });
});
