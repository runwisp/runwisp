// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Config for the README TUI showcase clip (`bun run demo-tui`). Replays a
// single continuous PTY stream captured by the Go harness
// (apps/runwisp tests/e2e TestCaptureTUIDemo) into a real xterm.js terminal
// (tui.demo.ts) and captures lossless PNG frames via a DevTools screencast
// (screencast.ts); scripts/encode-demo-video.sh builds the animated WebP from
// those. Unlike playwright.demo-video.config.ts (the Web UI clip), the replay
// only ever reads a captured file, no daemon involved, so there is no
// webServer/globalSetup/globalTeardown. NOT part of `bun run ci`, regenerated
// on demand, committed as a docs asset.

import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
    testDir: "./e2e/screenshots",
    testMatch: "**/tui.demo.ts",
    fullyParallel: false,
    workers: 1,
    retries: 0,
    reporter: "list",
    timeout: 60_000,
    // Deterministic home for the captured frames so the encode script finds them.
    outputDir: "./test-results/tui-demo",

    use: {
        bypassCSP: true,
        ...devices["Desktop Chrome"],
        // Fallback only: tui.demo.ts shrinks the real viewport to the rendered
        // window chrome's own size right after the terminal mounts, so the
        // screencast (which captures the whole viewport, unlike
        // tui.screenshots.ts's per-element screenshot) carries no blank margin.
        viewport: { width: 1440, height: 900 },
        // 2x so the page renders (and the screencast downsamples) supersampled,
        // for crisp text in the captured frames, matching tui.screenshots.ts.
        deviceScaleFactor: 2,
        video: "off",
    },

    projects: [{ name: "tui-demo", use: {} }],
});
