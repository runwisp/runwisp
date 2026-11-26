// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { StoredSet } from "./stored-flag.svelte";

const KEY = "test:set";

describe("StoredSet", () => {
    let store: Map<string, string>;

    beforeEach(() => {
        store = new Map();
        vi.stubGlobal("localStorage", {
            getItem: (k: string) => store.get(k) ?? null,
            setItem: (k: string, v: string) => store.set(k, v),
            removeItem: (k: string) => store.delete(k),
        });
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it("toggles values and survives a reload", () => {
        const set = new StoredSet(KEY);
        set.toggle("Backups");
        expect(set.has("Backups")).toBe(true);
        expect(new StoredSet(KEY).has("Backups")).toBe(true);

        set.toggle("Backups");
        expect(set.has("Backups")).toBe(false);
        expect(store.has(KEY)).toBe(false);
    });

    it("starts empty on a corrupt or foreign value", () => {
        store.set(KEY, "{not json");
        expect(new StoredSet(KEY).has("x")).toBe(false);
        store.set(KEY, JSON.stringify([1, "Backups"]));
        expect(new StoredSet(KEY).has("Backups")).toBe(true);
    });
});
