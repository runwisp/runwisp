// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { type Locator, type Page } from "@playwright/test";
import { test, expect } from "./fixtures/test-base";
import {
    markAllReadViaAPI,
    triggerRunViaAPI,
    waitForUnreadNotification,
    waitForCoalescedCount,
} from "./fixtures/api";

// These specs use the daemon directly so they isolate "did a notification
// surface" from "did the trigger UI flow work" (the latter is
// task-execution.spec.ts).
//
// The bell badge updates only via a forward-only SSE stream with no replay, so
// asserting it right after an API-triggered failure races the live push and
// drops events under CI load. Instead we trigger, confirm the backend persisted
// the notification (waitForUnreadNotification / waitForCoalescedCount), and only
// then load the page — the badge and list render deterministically from the
// initial fetch rather than the live stream.

const bell = (page: Page): Locator => page.getByRole("button", { name: "Notifications" });
const popover = (page: Page): Locator => page.getByRole("dialog", { name: "Notifications" });

test.describe("notifications", () => {
    test.setTimeout(process.env.CI ? 60_000 : 30_000);
    test("failed run surfaces in the bell badge and popover", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await markAllReadViaAPI(page, daemonState.token);

        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await waitForUnreadNotification(page, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        const badge = bell(page).locator("span[aria-label*='unread']");
        await expect(badge).toBeVisible();

        await bell(page).click();
        await expect(popover(page)).toBeVisible();

        const item = popover(page)
            .locator('[data-testid="notification-item"]')
            .filter({ hasText: "fail-task" })
            .first();
        await expect(item).toBeVisible();
    });

    test("repeated failures coalesce into a single row with a count", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        // Wait until both failures have merged into one coalesced row before
        // loading the page, so the rendered count is deterministic.
        await waitForCoalescedCount(page, "fail-task", 2, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await bell(page).click();
        await expect(popover(page)).toBeVisible();

        const item = popover(page)
            .locator('[data-testid="notification-item"]')
            .filter({ hasText: "fail-task" })
            .first();
        // Coalescing produces a single fail-task row whose rhythm phrase
        // includes a "N×" multiplier as soon as count >= 2. Earlier specs
        // in the run may have already pushed the count up, so we assert
        // the multiplier shape rather than a specific number.
        await expect(item).toContainText(/\d+×/);

        const allFailTask = await popover(page)
            .locator('[data-testid="notification-item"]')
            .filter({ hasText: "fail-task" })
            .count();
        expect(allFailTask, "fail-task should produce exactly one coalesced row").toBe(1);
    });

    test("view all link navigates to /notifications and lists items", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await markAllReadViaAPI(page, daemonState.token);

        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await waitForUnreadNotification(page, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await expect(bell(page).locator("span[aria-label*='unread']")).toBeVisible();

        await bell(page).click();
        await expect(popover(page)).toBeVisible();

        await popover(page).getByRole("link", { name: "View all" }).click();

        await expect(page).toHaveURL(/\/notifications\/?$/);
        await expect(page.getByRole("heading", { name: "Notifications" })).toBeVisible();

        await expect(
            page
                .locator('[data-testid="notification-item"]')
                .filter({ hasText: "fail-task" })
                .first(),
        ).toBeVisible({
            timeout: 5_000,
        });
    });

    test("mark all read clears the badge", async ({ authenticatedPage: page, daemonState }) => {
        await markAllReadViaAPI(page, daemonState.token);

        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await waitForUnreadNotification(page, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        const badge = bell(page).locator("span[aria-label*='unread']");
        await expect(badge).toBeVisible();

        await bell(page).click();
        await popover(page).getByRole("button", { name: "Mark all read" }).click();

        await expect(badge).toBeHidden({ timeout: 5_000 });
    });
});
