// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

interface BrowserStub {
    media: { matches: boolean };
    classList: Set<string>;
    fireMediaChange: () => void;
}

function stubBrowser(
    options: {
        matches?: boolean;
        storage?: Record<string, string>;
        throwOnStorage?: boolean;
    } = {},
): BrowserStub {
    const { matches = false, storage = {}, throwOnStorage = false } = options;
    const listeners: (() => void)[] = [];
    const media = { matches };
    const classList = new Set<string>();

    vi.stubGlobal("window", {});
    vi.stubGlobal("matchMedia", () => ({
        get matches() {
            return media.matches;
        },
        addEventListener: (_event: string, cb: () => void) => {
            listeners.push(cb);
        },
    }));
    vi.stubGlobal("localStorage", {
        getItem: (key: string) => {
            if (throwOnStorage) throw new Error("blocked");
            return storage[key] ?? null;
        },
        setItem: (key: string, value: string) => {
            if (throwOnStorage) throw new Error("blocked");
            storage[key] = value;
        },
    });
    vi.stubGlobal("document", {
        documentElement: {
            classList: {
                toggle: (cls: string, on: boolean) => {
                    if (on) classList.add(cls);
                    else classList.delete(cls);
                },
            },
        },
    });

    return {
        media,
        classList,
        fireMediaChange: () => {
            listeners.forEach((cb) => {
                cb();
            });
        },
    };
}

describe("themeStore", () => {
    beforeEach(() => {
        vi.resetModules();
    });
    afterEach(() => {
        vi.unstubAllGlobals();
    });

    it("degrades outside a browser", async () => {
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.preference).toBe("auto");
        expect(themeStore.resolved).toBe("light");
    });

    it("resolves auto to the OS preference", async () => {
        stubBrowser({ matches: true });
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.resolved).toBe("dark");
    });

    it("reads a stored preference over auto", async () => {
        stubBrowser({ storage: { "runwisp:theme": "dark" } });
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.preference).toBe("dark");
        expect(themeStore.resolved).toBe("dark");
    });

    it("falls back to the legacy storage key", async () => {
        stubBrowser({ storage: { "runwisp-theme": "light" } });
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.preference).toBe("light");
    });

    it("ignores an invalid stored value", async () => {
        stubBrowser({ storage: { "runwisp:theme": "purple" } });
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.preference).toBe("auto");
    });

    it("re-resolves live when the OS preference changes while in auto", async () => {
        const env = stubBrowser({ matches: false });
        const { themeStore } = await import("./theme.svelte.js");
        expect(themeStore.resolved).toBe("light");
        env.media.matches = true;
        env.fireMediaChange();
        expect(themeStore.resolved).toBe("dark");
    });

    it("ignores OS changes once a manual preference is set", async () => {
        const env = stubBrowser({ matches: false });
        const { themeStore } = await import("./theme.svelte.js");
        themeStore.set("light");
        env.media.matches = true;
        env.fireMediaChange();
        expect(themeStore.resolved).toBe("light");
    });

    it("persists an explicit preference and toggles the dark class", async () => {
        const env = stubBrowser();
        const { themeStore } = await import("./theme.svelte.js");
        themeStore.set("dark");
        expect(themeStore.resolved).toBe("dark");
        expect(env.classList.has("dark")).toBe(true);
        expect(localStorage.getItem("runwisp:theme")).toBe("dark");

        themeStore.set("light");
        expect(env.classList.has("dark")).toBe(false);
    });

    it("still applies the preference for this page when storage write throws", async () => {
        stubBrowser({ throwOnStorage: true });
        const { themeStore } = await import("./theme.svelte.js");
        expect(() => {
            themeStore.set("dark");
        }).not.toThrow();
        expect(themeStore.resolved).toBe("dark");
    });
});
