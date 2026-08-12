// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test as base, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./daemon-state.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, "../.state.json");

// Must match internal/server/auth.CookieName.
const AUTH_COOKIE_NAME = "runwisp_jwt";

let cachedState: DaemonState | undefined;

async function loadDaemonState(): Promise<DaemonState> {
    if (cachedState) return cachedState;
    const raw = await readFile(STATE_PATH, "utf-8");
    cachedState = JSON.parse(raw) as DaemonState;
    return cachedState;
}

/**
 * Extended test fixture that provides an authenticated page.
 * The daemon authenticates the browser via an HttpOnly session cookie (the JWT
 * is never exposed to page JS), so the fixture seeds that cookie into the
 * browser context before navigation, mirroring a real logged-in session.
 */
export const test = base.extend<{
    daemonState: DaemonState;
    authenticatedPage: Page;
}>({
    daemonState: async ({}, use) => {
        const state = await loadDaemonState();
        await use(state);
    },

    authenticatedPage: async ({ page }, use) => {
        const state = await loadDaemonState();

        // Seed the session cookie so API/SSE requests carry it, exactly as a
        // real login would. Path "/" so it rides every request the tests make.
        await page.context().addCookies([
            {
                name: AUTH_COOKIE_NAME,
                value: state.token,
                url: `http://127.0.0.1:${String(state.port)}`,
                httpOnly: true,
                sameSite: "Strict",
            },
        ]);

        await use(page);
    },
});

export { expect } from "@playwright/test";
