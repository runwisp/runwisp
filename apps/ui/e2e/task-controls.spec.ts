// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";

// autostart = false: the service starts out stopped.
const SERVICE = "idle-service";

test.describe("task header controls", () => {
    // The daemon is shared across specs; leave the service stopped as it began.
    test.afterEach(async ({ authenticatedPage: page, daemonState }) => {
        await page.request.post(`/api/tasks/${SERVICE}/stop`, {
            headers: { Authorization: `Bearer ${daemonState.token}` },
        });
    });

    test("the dashboard marks a stopped service Stopped", async ({ authenticatedPage: page }) => {
        await page.goto("/");
        const card = page
            .getByRole("main")
            .getByRole("button")
            .filter({ hasText: SERVICE })
            .filter({ hasText: "Stopped" });
        await expect(card).toHaveCount(1);
    });

    test("a service starts and stops from the header without opening a run", async ({
        authenticatedPage: page,
    }) => {
        await page.goto(`/tasks/${SERVICE}`);
        const header = page.getByRole("main").locator("header");
        await expect(header.getByRole("button", { name: "Stop service" })).toHaveCount(0);

        await header.getByRole("button", { name: "Start service" }).click();
        await page.getByRole("dialog").getByRole("button", { name: "Start Now" }).click();
        await expect(page.getByText(`Starting "${SERVICE}"`)).toBeVisible();

        await expect(header.getByRole("button", { name: "Restart service" })).toBeVisible();
        await header.getByRole("button", { name: "Stop service" }).click();
        await page.getByRole("dialog").getByRole("button", { name: "Stop Now" }).click();
        await expect(page.getByText(`Stopped "${SERVICE}"`)).toBeVisible();

        await expect(header.getByRole("button", { name: "Start service" })).toBeVisible();
        await expect(header.getByRole("button", { name: "Stop service" })).toHaveCount(0);
    });
});
