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

    test("Pause in the task strip pauses the schedule, which survives a reload", async ({
        authenticatedPage: page,
    }) => {
        await page.goto(`/tasks/${TASK}`);
        const strip = page.getByTestId("task-strip");
        const state = strip.getByTestId("task-state");
        await expect(state).toHaveText("Scheduled");
        await expect(strip.getByTestId("task-sentence")).toContainText("next run in");

        await strip.getByRole("button", { name: "Pause", exact: true }).click();
        await expect(page.getByText(`Paused the schedule of "${TASK}"`)).toBeVisible();
        await expect(state).toHaveText("Paused");

        await page.reload();
        await expect(state).toHaveText("Paused");

        await strip.getByRole("button", { name: "Resume schedule" }).click();
        await expect(state).toHaveText("Scheduled");
        await expect(strip.getByRole("button", { name: "Pause", exact: true })).toBeVisible();
    });

    test("Undo in the toast lifts the pause again", async ({ authenticatedPage: page }) => {
        await page.goto(`/tasks/${TASK}`);
        const strip = page.getByTestId("task-strip");
        const state = strip.getByTestId("task-state");

        await strip.getByRole("button", { name: "Pause", exact: true }).click();
        await expect(state).toHaveText("Paused");

        await page.getByRole("button", { name: "Undo" }).click();
        await expect(state).toHaveText("Scheduled");
    });

    test("a task without a cron schedule is Manual only, with no Pause", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/echo-task");
        const strip = page.getByTestId("task-strip");
        await expect(strip.getByTestId("task-state")).toHaveText("Manual only");
        await expect(strip.getByRole("button", { name: "Pause", exact: true })).toHaveCount(0);
    });
});
