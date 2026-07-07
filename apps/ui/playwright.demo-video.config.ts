// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Config for the README/social showcase clip (`bun run demo-video`). Like
// playwright.screenshots.config.ts it boots the rich demo-seeded daemon and
// drives a single continuous scripted tour (web-ui.demo-video.ts). Instead of
// Playwright's lossy .webm, the tour captures lossless PNG frames via a DevTools
// screencast (see screencast.ts); scripts/encode-demo-video.sh builds the
// animated WebP + MP4 from those in a single generation. NOT part of
// `bun run ci` — regenerated on demand, committed as a docs asset.

import { defineConfig, devices } from "@playwright/test";

const port = Number(process.env.SCREENSHOT_PORT) || 19299;

// 16:10 canvas. The DevTools screencast emits frames at this CSS-pixel size, but
// deviceScaleFactor:2 makes Chrome render the page at 2× and downsample into each
// frame — supersampled anti-aliasing, so text stays crisp without a huge asset.
const WIDTH = 1280;
const HEIGHT = 800;

export default defineConfig({
    testDir: "./e2e/screenshots",
    testMatch: "**/*.demo-video.ts",
    fullyParallel: false,
    workers: 1,
    retries: 0,
    reporter: "list",
    timeout: 120_000,
    // Deterministic home for the captured frames so the encode script finds them.
    outputDir: "./test-results/demo-video",

    // Reuse the demo-seeded daemon boot + teardown from the screenshot harness.
    globalSetup: "./e2e/screenshots/global-setup.ts",
    globalTeardown: "./e2e/global-teardown.ts",

    use: {
        baseURL: `http://127.0.0.1:${port}`,
        bypassCSP: true,
        ...devices["Desktop Chrome"],
        viewport: { width: WIDTH, height: HEIGHT },
        // 2× so the page renders (and the screencast downsamples) supersampled —
        // crisp text in the captured frames. Frame size stays WIDTH×HEIGHT.
        deviceScaleFactor: 2,
        // Light theme, per the showcase art direction.
        colorScheme: "light",
        // No Playwright video — frames come from the lossless DevTools screencast.
        video: "off",
    },

    projects: [{ name: "demo-video", use: {} }],
});
