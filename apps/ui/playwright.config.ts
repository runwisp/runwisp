// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.E2E_PORT) || 19287;

export default defineConfig({
    testDir: "./e2e",
    fullyParallel: false,
    workers: 1,
    retries: process.env.CI ? 1 : 0,
    reporter: process.env.CI ? "github" : "list",
    timeout: process.env.CI ? 30_000 : 5_000,

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
            testMatch: ["dashboard.spec.ts", "task-execution.spec.ts", "notifications.spec.ts"],
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
