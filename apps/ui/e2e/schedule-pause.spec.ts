// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";

const TASK = "nightly-task";

test.describe("schedule pause", () => {
    // The daemon is shared across specs and the pause is persisted; never leave
    // it behind.
    test.afterEach(async ({ authenticatedPage: page, daemonState }) => {
        const resumed = await page.request.post(`/api/tasks/${TASK}/resume`, {
            headers: { Authorization: `Bearer ${daemonState.token}` },
        });
        expect(resumed.status()).toBe(204);
    });

    test("the header Pause button pauses the schedule, which survives a reload", async ({
        authenticatedPage: page,
    }) => {
        await page.goto(`/tasks/${TASK}`);
        const header = page.getByRole("main").locator("header");
        const chip = page.getByTestId("schedule-chip");
        await expect(chip).toBeVisible();
        await expect(chip).not.toContainText("Schedule paused");

        await chip.click();
        await expect(page.getByText(/^Next run in /)).toBeVisible();
        await page.keyboard.press("Escape");

        await header.getByRole("button", { name: "Pause schedule" }).click();
        await expect(page.getByText(`Paused the schedule of "${TASK}"`)).toBeVisible();
        await expect(chip).toContainText("Schedule paused");

        await page.reload();
        await expect(chip).toContainText("Schedule paused");

        await header.getByRole("button", { name: "Resume schedule" }).click();
        await expect(chip).not.toContainText("Schedule paused");
        await expect(header.getByRole("button", { name: "Pause schedule" })).toBeVisible();
    });

    test("Undo in the toast lifts the pause again", async ({ authenticatedPage: page }) => {
        await page.goto(`/tasks/${TASK}`);
        const chip = page.getByTestId("schedule-chip");

        await page
            .getByRole("main")
            .locator("header")
            .getByRole("button", { name: "Pause schedule" })
            .click();
        await expect(chip).toContainText("Schedule paused");

        await page.getByRole("button", { name: "Undo" }).click();
        await expect(chip).not.toContainText("Schedule paused");
    });

    test("a task without a cron schedule has no chip and no Pause", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task" })).toBeVisible();
        await expect(page.getByTestId("schedule-chip")).toHaveCount(0);
        await expect(page.getByRole("button", { name: "Pause schedule" })).toHaveCount(0);
    });
});
