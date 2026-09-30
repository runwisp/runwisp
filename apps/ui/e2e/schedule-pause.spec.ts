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

    test("the top bar chip pauses the schedule, which survives a reload", async ({
        authenticatedPage: page,
    }) => {
        await page.goto(`/tasks/${TASK}`);
        const chip = page.getByTestId("schedule-chip");
        await expect(chip).toBeVisible();
        await expect(chip).not.toContainText("Schedule paused");

        await chip.click();
        await expect(page.getByText(/^Next run in /)).toBeVisible();
        await page.getByRole("button", { name: "Pause schedule" }).click();
        await expect(page.getByText(`Paused the schedule of "${TASK}"`)).toBeVisible();
        await expect(chip).toContainText("Schedule paused");

        await page.reload();
        await expect(chip).toContainText("Schedule paused");

        await chip.click();
        await page.getByRole("button", { name: "Resume schedule" }).click();
        await expect(chip).not.toContainText("Schedule paused");
    });

    test("Undo in the toast lifts the pause again", async ({ authenticatedPage: page }) => {
        await page.goto(`/tasks/${TASK}`);
        const chip = page.getByTestId("schedule-chip");

        await chip.click();
        await page.getByRole("button", { name: "Pause schedule" }).click();
        await expect(chip).toContainText("Schedule paused");

        await page.getByRole("button", { name: "Undo" }).click();
        await expect(chip).not.toContainText("Schedule paused");
    });

    test("a task without a cron schedule has no chip", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task" })).toBeVisible();
        await expect(page.getByTestId("schedule-chip")).toHaveCount(0);
    });
});
