// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";

export type ThemePreference = "auto" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

// Keep this key byte-for-byte in sync with the no-flash script in app.html.
const THEME_STORAGE_KEY = "runwisp-theme";

function isPreference(value: string | null): value is ThemePreference {
    return value === "auto" || value === "light" || value === "dark";
}

function createThemeStore() {
    let preference = $state<ThemePreference>("auto");
    let resolved = $state<ResolvedTheme>("light");

    const media = browser ? globalThis.matchMedia("(prefers-color-scheme: dark)") : null;

    function resolve(pref: ThemePreference): ResolvedTheme {
        if (pref === "auto") return media?.matches === true ? "dark" : "light";
        return pref;
    }

    function apply(): void {
        if (!browser) return;
        resolved = resolve(preference);
        document.documentElement.classList.toggle("dark", resolved === "dark");
    }

    if (browser) {
        const stored = localStorage.getItem(THEME_STORAGE_KEY);
        if (isPreference(stored)) preference = stored;
        apply();
        // Re-resolve live when the OS preference changes while in `auto`.
        media?.addEventListener("change", () => {
            if (preference === "auto") apply();
        });
    }

    return {
        get preference(): ThemePreference {
            return preference;
        },
        get resolved(): ResolvedTheme {
            return resolved;
        },
        set(pref: ThemePreference): void {
            preference = pref;
            if (browser) localStorage.setItem(THEME_STORAGE_KEY, pref);
            apply();
        },
    };
}

export const themeStore = createThemeStore();
