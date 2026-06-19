// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { type Locator, type Page } from "@playwright/test";
import { test, expect } from "./fixtures/test-base";
import { markAllReadViaAPI, triggerRunViaAPI } from "./fixtures/api";

// triggerRunViaAPI / markAllReadViaAPI are shared in fixtures/api.ts — we use
// the daemon directly so these specs isolate "did a notification surface" from
// "did the trigger UI flow work" (the latter is task-execution.spec.ts).

const bell = (page: Page): Locator => page.getByRole("button", { name: "Notifications" });
const popover = (page: Page): Locator => page.getByRole("dialog", { name: "Notifications" });

// Notification flow: REST trigger → run completes (~1s) → SSE push → store
// → DOM. The default 5s wall-clock can race the run; 30s is the same budget
// task-execution.spec.ts uses for FAILED status assertions.
test.describe("notifications", () => {
    test.setTimeout(process.env.CI ? 60_000 : 30_000);
    test("failed run surfaces in the bell badge and popover", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await markAllReadViaAPI(page, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await triggerRunViaAPI(page, "fail-task", daemonState.token);

        const badge = bell(page).locator("span[aria-label*='unread']");
        await expect(badge).toBeVisible({ timeout: 15_000 });

        await bell(page).click();
        await expect(popover(page)).toBeVisible();

        const item = popover(page)
            .locator('[data-testid="notification-item"]')
            .filter({ hasText: "fail-task" })
            .first();
        await expect(item).toBeVisible({ timeout: 5_000 });
    });

    test("repeated failures coalesce into a single row with a count", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await triggerRunViaAPI(page, "fail-task", daemonState.token);

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
        await expect(item).toContainText(/\d+×/, { timeout: 15_000 });

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

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await triggerRunViaAPI(page, "fail-task", daemonState.token);

        await expect(bell(page).locator("span[aria-label*='unread']")).toBeVisible({
            timeout: 15_000,
        });

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

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await triggerRunViaAPI(page, "fail-task", daemonState.token);

        const badge = bell(page).locator("span[aria-label*='unread']");
        await expect(badge).toBeVisible({ timeout: 15_000 });

        await bell(page).click();
        await popover(page).getByRole("button", { name: "Mark all read" }).click();

        await expect(badge).toBeHidden({ timeout: 5_000 });
    });
});
