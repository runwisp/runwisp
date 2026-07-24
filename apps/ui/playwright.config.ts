// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.E2E_PORT) || 19287;

export default defineConfig({
    testDir: "./e2e",
    fullyParallel: false,
    workers: 1,
    retries: process.env.CI ? 1 : 0,
    reporter: process.env.CI ? "github" : "list",
    // Several specs wait on a real task run or poll the API with explicit
    // per-assertion timeouts up to 30s (task-execution, frame-history,
    // run-lifecycle, runs-page, dashboard, notifications). The test-level
    // timeout is a hard ceiling over those — a shorter local default would
    // silently truncate them, so it must be at least as generous as CI's.
    timeout: 30_000,
    // Playwright's built-in default (5s) applies to every bare `expect(...)`
    // that doesn't pass its own `{ timeout }` — most assertions in this suite
    // (nav + render, no process wait). Under CPU contention that default is
    // too tight even for simple UI settling; specs that need more already ask
    // for it explicitly (10-30s), this just raises the un-annotated floor.
    expect: { timeout: 10_000 },

    globalSetup: "./e2e/global-setup.ts",
    globalTeardown: "./e2e/global-teardown.ts",

    use: {
        baseURL: `http://127.0.0.1:${port}`,
        bypassCSP: true,
        trace: "retain-on-failure",
        screenshot: "only-on-failure",
    },

    projects: [
        {
            name: "authenticated",
            testMatch: [
                "dashboard.spec.ts",
                "task-execution.spec.ts",
                "run-lifecycle.spec.ts",
                "runs-page.spec.ts",
                "notifications.spec.ts",
                "theme.spec.ts",
                "frame-history.spec.ts",
            ],
            use: { ...devices["Desktop Chrome"] },
        },
        {
            name: "auth-flow",
            testMatch: ["auth.spec.ts"],
            dependencies: ["authenticated"],
            use: { ...devices["Desktop Chrome"] },
        },
    ],
});
