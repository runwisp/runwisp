// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import { triggerRunViaAPI, waitForRunEnded } from "./fixtures/api";

// The fixture puts echo-task and fail-task in group "Smoke"; every other task
// falls under the default "Tasks" group.
test.describe("task groups", () => {
    test.beforeEach(async ({ authenticatedPage: page, daemonState }) => {
        const run = await triggerRunViaAPI(page, "fail-task", daemonState.token);
        await waitForRunEnded(page, "fail-task", run.id, daemonState.token);
    });

    test("a folded sidebar group stays folded and still shows its failures", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/");
        const sidebar = page.locator("#app-sidebar");
        const header = sidebar.getByRole("button", { name: /^Smoke/ });
        const echoLink = sidebar.getByRole("link", { name: "echo-task", exact: true });

        await expect(header).toHaveAttribute("aria-expanded", "true");
        await expect(echoLink).toBeVisible();

        await header.click();
        await expect(header).toHaveAttribute("aria-expanded", "false");
        await expect(echoLink).toHaveCount(0);
        await expect(header).toContainText("1 failing");

        await page.reload();
        await expect(header).toHaveAttribute("aria-expanded", "false");
        await expect(echoLink).toHaveCount(0);

        // A folded group keeps showing the task whose page is open.
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();
        await expect(echoLink).toBeVisible();
        await expect(sidebar.getByRole("link", { name: "fail-task", exact: true })).toHaveCount(0);
    });

    test("the overview filters by group without hiding failures elsewhere", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/");
        const main = page.getByRole("main");

        await main.getByRole("button", { name: "All groups" }).click();
        // The option list is portalled out of <main>.
        await page.getByRole("button", { name: /^Tasks \(\d+\)$/ }).click();
        await expect(page).toHaveURL(/\?group=Tasks$/);

        const tasksList = main
            .getByRole("heading", { name: "Tasks", exact: true })
            .locator(
                "xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' space-y-4 ')][1]",
            );
        await expect(tasksList.getByRole("button", { name: /slow-task/ })).toBeVisible();
        await expect(tasksList.getByRole("button", { name: /fail-task/ })).toHaveCount(0);

        // The attention panel above the list is global: fail-task stays on screen.
        await expect(main.getByRole("button", { name: /fail-task/ }).first()).toBeVisible();

        // The pick survives a reload.
        await page.reload();
        await expect(main.getByRole("button", { name: /^Tasks \(/ })).toBeVisible();
        await expect(tasksList.getByRole("button", { name: /fail-task/ })).toHaveCount(0);
    });
});
