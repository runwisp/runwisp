// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import { triggerRunViaAPI, waitForRunEnded } from "./fixtures/api";

test.describe("dashboard", () => {
    test("displays the dashboard with configured tasks", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await expect(page.getByText("Healthy tasks")).toBeVisible();
    });

    test("shows a card for every task in the config", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        // The fixture defines exactly these tasks — the grid must mirror the
        // TOML, not just render "some" cards.
        await expect(page.getByRole("button", { name: /echo-task/ })).toBeVisible();
        await expect(page.getByRole("button", { name: /fail-task/ })).toBeVisible();
        await expect(page.getByRole("button", { name: /slow-task/ })).toBeVisible();
        await expect(page.getByRole("button", { name: /timed-task/ })).toBeVisible();
    });

    test("displays system stats section", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(page.getByText("Online")).toBeVisible();
        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();
    });

    test("recent activity reflects a completed run", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        test.setTimeout(30_000);

        // Seed a real, finished run via the API, then load the dashboard and
        // assert the activity feed surfaces *that* run with its real outcome —
        // not merely that the "Recent activity" heading rendered.
        const run = await triggerRunViaAPI(page, "echo-task", daemonState.token);
        await waitForRunEnded(page, "echo-task", run.id, daemonState.token);

        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await expect(
            page.getByRole("heading", { name: "Recent activity", exact: true }),
        ).toBeVisible();
        await expect(page.getByRole("heading", { name: "Running now", exact: true })).toBeVisible();

        // An activity row for echo-task carrying a success status badge.
        await expect(
            page.getByRole("button", { name: /echo-task[\s\S]*success/i }).first(),
        ).toBeVisible();
    });

    test("clicking task card navigates to task detail", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        // The dashboard can show echo-task in several places (recent activity,
        // side panels). Scope to the "Tasks" grid so we click the task *card*.
        const tasksGrid = page
            .getByRole("heading", { name: "Tasks", exact: true })
            .locator(
                "xpath=ancestor::div[contains(concat(' ', normalize-space(@class), ' '), ' space-y-4 ')][1]",
            );
        await tasksGrid.getByRole("button", { name: /echo-task/ }).click();
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();
    });
});
