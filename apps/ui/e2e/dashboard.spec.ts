// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";

test.describe("dashboard", () => {
    test("displays the dashboard with configured tasks", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();
        await expect(page.getByText("Healthy tasks")).toBeVisible();
    });

    test("shows all task cards", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(page.getByRole("button", { name: /echo-task/ })).toBeVisible();
        await expect(page.getByRole("button", { name: /fail-task/ })).toBeVisible();
        await expect(page.getByRole("button", { name: /slow-task/ })).toBeVisible();
    });

    test("displays system stats section", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(page.getByText("Online")).toBeVisible();
        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();
    });

    test("displays recent activity section", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await expect(
            page.getByRole("heading", { name: "Recent activity", exact: true }),
        ).toBeVisible();
        await expect(page.getByRole("heading", { name: "Running now", exact: true })).toBeVisible();
    });

    test("clicking task card navigates to task detail", async ({ authenticatedPage: page }) => {
        await page.goto("/");

        await page.getByRole("button", { name: /echo-task/ }).click();
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();
    });
});
