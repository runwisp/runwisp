// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Generates the Web UI screenshots embedded in the docs (apps/docs). Each page
// is captured in both light and dark themes, driven against the demo-seeded
// daemon from screenshots/global-setup.ts. Run via `bun run screenshots`.
//
// Theme: the UI defaults to "Auto", which follows the OS color scheme, so
// `page.emulateMedia({ colorScheme })` selects the theme without touching the
// in-app menu — see theme.spec.ts for the same mechanism.
//
// These are illustrative, on-demand, committed assets. Relative timestamps and
// the exact set of seeded runs drift between regenerations; that's expected.

import { type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test, expect } from "../fixtures/test-base";
import { triggerRunViaAPI, waitForRunEnded, waitForUnreadNotification } from "../fixtures/api";
import type { Run } from "@runwisp/common";

const __dirname = dirname(fileURLToPath(import.meta.url));
const OUT_DIR = resolve(__dirname, "../../../docs/src/assets/screenshots");

const THEMES = ["light", "dark"] as const;

// A task that always succeeds with a tidy multi-line log — good for the
// "task detail" tour shot.
const SUCCESS_TASK = "backup-postgres";
// Runs every 5 minutes, so it's always among the most-recent runs — a stable
// anchor for the /runs list (which shows newest-first, not backup-postgres).
const RECENT_TASK = "healthcheck-api";
// Times out ~1/6 of the time (2s budget, occasional 5s stall) with no retry, so
// each timeout is terminal and fast. run.timeout hits the demo's in-app
// catch-all (global_notifiers = ["inapp"]) and lights up the bell.
const FLAKY_TASK = "cache-prewarm-probe";

async function settle(page: Page): Promise<void> {
    // The UI holds an SSE connection open, so `networkidle` never fires; instead
    // give layout, sparkline draws, and theme `transition-colors` time to land.
    await page.waitForTimeout(800);
}

function shoot(page: Page, name: string): Promise<Buffer> {
    return page.screenshot({ path: join(OUT_DIR, `${name}.png`) });
}

async function unreadCount(page: Page, token: string): Promise<number> {
    const res = await page.request.get("/api/notifications/unread-count", {
        headers: { Authorization: `Bearer ${token}` },
    });
    if (!res.ok()) return 0;
    return ((await res.json()) as { count: number }).count;
}

/** Drive enough live failures that the in-app bell has unread notifications. */
async function ensureNotifications(page: Page, token: string): Promise<void> {
    // Trigger one at a time and wait for each to end: cache-prewarm-probe's
    // concurrency policy would otherwise skip overlapping triggers (a skip is
    // not a failure, so it wouldn't notify). Repeated timeouts coalesce into a
    // single "N×" row, so the unread *count* stays at 1 — we instead do a small
    // minimum batch to build up a believable N, then stop once the bell is lit.
    const MIN_TRIGGERS = 12;
    const MAX_TRIGGERS = 40;
    for (let i = 0; i < MAX_TRIGGERS; i++) {
        const run = await triggerRunViaAPI(page, FLAKY_TASK, token).catch(() => null);
        if (run) await waitForRunEnded(page, FLAKY_TASK, run.id, token, 8_000).catch(() => {});
        if (i + 1 >= MIN_TRIGGERS && (await unreadCount(page, token)) >= 1) break;
    }
    await waitForUnreadNotification(page, token);
}

async function findFailedRun(page: Page, token: string): Promise<Run> {
    const res = await page.request.get(
        "/api/runs?status=failed&limit=1&sort_field=start_at&sort_direction=desc",
        { headers: { Authorization: `Bearer ${token}` } },
    );
    expect(res.ok(), "list failed runs").toBeTruthy();
    const run = ((await res.json()) as { runs: Run[] }).runs[0];
    expect(run, "demo seed should contain a failed run").toBeTruthy();
    return run;
}

test.beforeAll(async () => {
    await mkdir(OUT_DIR, { recursive: true });
});

// The dashboard pages: authenticated, captured in both themes.
test("overview, runs, task detail", async ({ authenticatedPage: page, daemonState }) => {
    const failed = await findFailedRun(page, daemonState.token);

    for (const theme of THEMES) {
        await page.emulateMedia({ colorScheme: theme });

        // Overview (/)
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();
        // Wait for the recent-activity feed to populate so the capture isn't
        // taken while /api/runs is mid-retry during the boot burst.
        await expect(page.getByRole("main").getByText(RECENT_TASK).first()).toBeVisible({
            timeout: 15_000,
        });
        await settle(page);
        await shoot(page, `web-ui-overview-${theme}`);

        // All runs (/runs)
        await page.goto("/runs");
        await expect(page.getByRole("main").getByText("Runs", { exact: true })).toBeVisible();
        // Generous timeout: rides out the brief boot-burst window where /api/runs
        // can 500 under seed + scheduler load before AsyncData retries succeed.
        await expect(page.getByRole("main").getByText(RECENT_TASK).first()).toBeVisible({
            timeout: 15_000,
        });
        await settle(page);
        await shoot(page, `web-ui-runs-${theme}`);

        // Task detail — a finished, successful run
        await page.goto(`/tasks/${SUCCESS_TASK}`);
        await expect(page.getByRole("heading", { name: SUCCESS_TASK, level: 1 })).toBeVisible();
        await expect(page.getByText("SUCCESS", { exact: true }).first()).toBeVisible();
        await settle(page);
        await shoot(page, `web-ui-task-detail-${theme}`);

        // Task detail — a failed run (selected via the run-id path segment)
        await page.goto(`/tasks/${failed.task_name}/${failed.id}`);
        await expect(page.getByRole("heading", { name: failed.task_name, level: 1 })).toBeVisible();
        await expect(page.getByText("FAILED", { exact: true }).first()).toBeVisible();
        await settle(page);
        await shoot(page, `web-ui-task-failed-${theme}`);
    }
});

// The login modal: a fresh (unauthenticated) page, so no token is injected.
test("login modal", async ({ page }) => {
    for (const theme of THEMES) {
        await page.emulateMedia({ colorScheme: theme });
        await page.goto("/");
        await expect(page.getByRole("dialog", { name: "RunWisp" })).toBeVisible({
            timeout: 15_000,
        });
        await settle(page);
        await shoot(page, `web-ui-login-${theme}`);
    }
});

// Notifications: generate live failures first, then capture the bell popover and
// the full /notifications page. Runs last so the triggered failures don't show
// up in the overview / runs shots above.
test("notifications", async ({ authenticatedPage: page, daemonState }) => {
    await ensureNotifications(page, daemonState.token);

    const bell = page.getByRole("button", { name: "Notifications" });
    const popover = page.getByRole("dialog", { name: "Notifications" });

    for (const theme of THEMES) {
        await page.emulateMedia({ colorScheme: theme });

        // Bell popover — reload so the badge/list render from the initial fetch
        // rather than racing the live SSE push (see notifications.spec.ts).
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await bell.click();
        await expect(popover).toBeVisible();
        await expect(popover.locator('[data-testid="notification-item"]').first()).toBeVisible();
        await settle(page);
        await shoot(page, `web-ui-notifications-popover-${theme}`);

        // Full notifications page
        await page.goto("/notifications");
        await expect(
            page.getByRole("heading", { name: "Notifications", exact: true }),
        ).toBeVisible();
        await expect(page.locator('[data-testid="notification-item"]').first()).toBeVisible();
        await settle(page);
        await shoot(page, `web-ui-notifications-page-${theme}`);
    }
});
