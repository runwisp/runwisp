// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type ThemePreference = "auto" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

// Shared by every RunWisp surface. Keep byte-for-byte in sync with the
// no-flash <head> scripts in each app's app.html / layout.
export const THEME_STORAGE_KEY = "runwisp:theme";
// The daemon dashboard used this key before the store moved here.
const LEGACY_STORAGE_KEY = "runwisp-theme";

function isPreference(value: string | null): value is ThemePreference {
    return value === "auto" || value === "light" || value === "dark";
}

function readStored(): string | null {
    try {
        return localStorage.getItem(THEME_STORAGE_KEY) ?? localStorage.getItem(LEGACY_STORAGE_KEY);
    } catch {
        return null; // private mode / embedded contexts
    }
}

function createThemeStore() {
    const browser = typeof window !== "undefined";
    let preference = $state<ThemePreference>("auto");
    let resolved = $state<ResolvedTheme>("light");

    const media = browser ? globalThis.matchMedia("(prefers-color-scheme: dark)") : null;

    function apply(): void {
        if (!browser) return;
        resolved =
            preference === "auto" ? (media?.matches === true ? "dark" : "light") : preference;
        document.documentElement.classList.toggle("dark", resolved === "dark");
    }

    if (browser) {
        const stored = readStored();
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
            try {
                localStorage.setItem(THEME_STORAGE_KEY, pref);
            } catch {
                // not persisted; still applied for this page
            }
            apply();
        },
    };
}

export const themeStore = createThemeStore();
