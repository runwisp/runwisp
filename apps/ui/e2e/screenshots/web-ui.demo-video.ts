// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The RunWisp Web UI showcase clip (README hero + social). One continuous,
// light-theme, ~15s tour with a visible synthetic cursor, driven against the
// demo-seeded daemon from screenshots/global-setup.ts. Captures lossless PNG
// frames via a DevTools screencast (screencast.ts); scripts/encode-demo-video.sh
// turns them into an animated WebP + MP4. Regenerated on demand — not part of
// `bun run ci`.
//
// Story beats (optimized for low attention span, front-loaded value):
//   1. Overview — stat cards + live CPU/memory sparklines.
//   2. Open a task — click backup-postgres in the sidebar.
//   3. Trigger a run — Run → Run Now.
//   4. Live streaming console — the pg_dump progress bar fills in place (money shot).
//   5. Why it failed — back to the dashboard, click a failed run in Recent
//      activity; its task page opens with that run scrolled into view: red
//      panel, exit code, captured error output.
//   6. Loop back to the overview for a seamless repeat.

import { type Page } from "@playwright/test";
import { request as apiRequest } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test, expect } from "../fixtures/test-base";
import { DemoCursor } from "./cursor-overlay";
import { Screencast } from "./screencast";
import { isFailureEndReason, type Run } from "@runwisp/common";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, "../.state.json");
// Lossless PNG frames land here (under the gitignored test-results/ dir);
// scripts/encode-demo-video.sh reads frames.txt from it. Must match that script.
const FRAMES_DIR = resolve(__dirname, "../../test-results/demo-video/frames");

// A scheduled task whose run streams a single-line `\r` progress bar (pg_dump %
// then s3 upload) — the live-console "money shot".
const LIVE_TASK = "backup-postgres";
// Fails intermittently (exit 1) with a fast run; we trigger it off-camera in
// beforeAll until one run genuinely fails, so a real failure is among the newest
// runs shown in Recent activity for beat 5. (Its auto-retry may land a later
// success above it in the rail — which is fine: RunSelection scrolls the failed
// run into view when we open it.)
const FAIL_TASK = "healthcheck-api";

/** A short human-paced beat. */
function beat(page: Page, ms: number): Promise<void> {
    return page.waitForTimeout(ms);
}

// Generate one genuine recent failure *before* the recording context exists, so
// the failure-hunting loop adds no dead time to the captured video.
test.beforeAll(async () => {
    const { port, token } = JSON.parse(await readFile(STATE_PATH, "utf-8")) as {
        port: number;
        token: string;
    };
    const ctx = await apiRequest.newContext({
        baseURL: `http://127.0.0.1:${port}`,
        extraHTTPHeaders: { Authorization: `Bearer ${token}` },
    });
    try {
        for (let i = 0; i < 40; i++) {
            const res = await ctx.post(`/api/tasks/${FAIL_TASK}/run`);
            if (!res.ok()) continue;
            const started = (await res.json()) as Run;

            // Poll this run to its terminal phase.
            let ended: Run | undefined;
            for (let p = 0; p < 60; p++) {
                const get = await ctx.get(`/api/tasks/${FAIL_TASK}/runs/${started.id}`);
                if (get.ok()) {
                    const run = (await get.json()) as Run;
                    if (run.status === "ended") {
                        ended = run;
                        break;
                    }
                }
                await new Promise((r) => setTimeout(r, 150));
            }
            if (ended && isFailureEndReason(ended.end_reason)) return; // a real failure landed
        }
    } finally {
        await ctx.dispose();
    }
});

test("web ui showcase tour", async ({ authenticatedPage: page }) => {
    const cursor = await DemoCursor.install(page);

    // ── Beat 1: Overview ─────────────────────────────────────────────────────
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();
    // Let the recent-activity feed + sparklines populate before the cursor enters.
    await expect(page.getByRole("main").getByText(LIVE_TASK).first()).toBeVisible({
        timeout: 15_000,
    });
    await cursor.settle();
    // Let the overview fully paint before capture begins, so the first recorded
    // frame is the clean loop anchor (populated dashboard, cursor at home) — the
    // "Connecting…" load flash never enters the clip.
    await beat(page, 350);
    const screencast = await Screencast.start(page, FRAMES_DIR);
    await beat(page, 1100);

    // ── Beat 2: Open a task ──────────────────────────────────────────────────
    const sidebarLink = page.getByRole("navigation").getByRole("link", { name: LIVE_TASK });
    await cursor.click(sidebarLink);
    await expect(page.getByRole("heading", { name: LIVE_TASK, level: 1 })).toBeVisible();
    await cursor.settle();
    await beat(page, 600);

    // ── Beat 3: Trigger a run ────────────────────────────────────────────────
    const runButton = page.getByRole("button", { name: /^Run( task)?$/ }).first();
    await cursor.click(runButton);
    const runNow = page.getByRole("button", { name: "Run Now" });
    await expect(runNow).toBeVisible();
    await cursor.click(runNow);

    // ── Beat 4: Live streaming console (money shot) ──────────────────────────
    // The panel switches to the new live run; the pg_dump progress bar streams
    // and redraws in place. Hold long enough to watch it fill and complete.
    await beat(page, 4200);

    // ── Beat 5: Why it failed ────────────────────────────────────────────────
    // Back to the dashboard, then click a failed run in Recent activity. It
    // opens on its task page with the run scrolled into view (see RunsList
    // scroll-to-selected): red spine, exit-code tile, captured error output.
    await cursor.click(
        page.getByRole("navigation").getByRole("link", { name: "Overview" }).first(),
    );
    await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
    await cursor.settle();
    await beat(page, 500);

    const failedRow = page
        .getByRole("main")
        .getByRole("button")
        .filter({ hasText: /failed/i })
        .first();
    await expect(failedRow).toBeVisible();
    await cursor.click(failedRow);
    await expect(page.getByText("FAILED", { exact: true }).first()).toBeVisible({
        timeout: 15_000,
    });
    await cursor.settle();
    // Glide across the outcome readout toward the error output, then hold.
    const exitCell = page.getByText("Exit", { exact: true }).first();
    await cursor.moveOver(exitCell).catch(() => {});
    await beat(page, 600);
    await cursor.moveTo(360, 560).catch(() => {});
    await beat(page, 1500);

    // ── Beat 6: Loop back ────────────────────────────────────────────────────
    // Return to the overview and glide the cursor back to its exact opening
    // position, so the last frame matches the first and the WebP loops cleanly.
    await cursor.click(
        page.getByRole("navigation").getByRole("link", { name: "Overview" }).first(),
    );
    await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
    await cursor.settle();
    await beat(page, 300);
    await cursor.home();
    // Let the final resting frame paint, then stop — stop() holds it for the
    // closing beat so the overview lingers before the loop wraps.
    await beat(page, 250);
    await screencast.stop(900);
    console.log(`[demo-video] captured ${screencast.frameCount} frames -> ${FRAMES_DIR}`);
});
