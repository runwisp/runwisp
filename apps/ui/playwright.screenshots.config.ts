// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Config for the docs screenshot generator (`bun run screenshots`). Separate
// from playwright.config.ts: it boots the rich demo-seeded daemon and runs only
// screenshots/web-ui.screenshots.ts, writing PNGs into apps/docs. It is NOT part
// of `bun run ci` — screenshots are regenerated on demand and committed.

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.SCREENSHOT_PORT) || 19299;

export default defineConfig({
    testDir: "./e2e/screenshots",
    testMatch: "**/*.screenshots.ts",
    fullyParallel: false,
    workers: 1,
    retries: 0,
    reporter: "list",
    // Generous: a spec drives several pages and triggers live runs to populate
    // the notification bell.
    timeout: 120_000,

    globalSetup: "./e2e/screenshots/global-setup.ts",
    globalTeardown: "./e2e/global-teardown.ts",

    use: {
        baseURL: `http://127.0.0.1:${port}`,
        bypassCSP: true,
        // A wide, retina-crisp canvas for docs. 2x keeps the PNGs sharp on
        // high-DPI displays without doubling the rendered layout.
        ...devices["Desktop Chrome"],
        viewport: { width: 1440, height: 900 },
        deviceScaleFactor: 2,
    },

    projects: [{ name: "screenshots", use: {} }],
});
