// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test as base, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./daemon-state.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, "../.state.json");

const AUTH_TOKEN_KEY = "runwisp_token";

let cachedState: DaemonState | undefined;

async function loadDaemonState(): Promise<DaemonState> {
    if (cachedState) return cachedState;
    const raw = await readFile(STATE_PATH, "utf-8");
    cachedState = JSON.parse(raw) as DaemonState;
    return cachedState;
}

/**
 * Extended test fixture that provides an authenticated page.
 * Uses `addInitScript` to inject the JWT into localStorage BEFORE any page
 * scripts execute, avoiding race conditions with the auth middleware.
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

        // Inject the token into localStorage before any page scripts run.
        // This prevents the auth middleware from seeing a missing token and
        // emitting 401 → token removal → auth modal.
        await page.addInitScript(
            ({ key, value }) => {
                localStorage.setItem(key, value);
            },
            { key: AUTH_TOKEN_KEY, value: state.token },
        );

        await use(page);
    },
});

export { expect } from "@playwright/test";
