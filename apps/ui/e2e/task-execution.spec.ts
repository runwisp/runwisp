// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";

test.describe("task execution", () => {
    test("trigger successful task and verify completion", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        // Wait for run detail panel to show SUCCESS badge (exact match avoids
        // the lowercase "success" in the run list which uses CSS capitalize)
        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        await expect(page.getByText("Code 0")).toBeVisible();

        await expect(page.getByText("echo-line-1")).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("echo-line-2")).toBeVisible();
    });

    test("trigger failing task and verify failure status", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/fail-task");

        await expect(page.getByRole("heading", { name: "fail-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        await expect(page.getByText("FAILED", { exact: true })).toBeVisible({ timeout: 30_000 });
        await expect(page.getByText("Code 1")).toBeVisible();
        await expect(page.getByText("fail-line-1")).toBeVisible({ timeout: 10_000 });
    });

    test("run appears in task run history", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // Run list should show at least one entry with a short ID
        await expect(page.getByText(/^#[A-Z0-9]/).first()).toBeVisible();
    });

    test("dashboard activity feed updates after task run", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();
        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // Navigate to dashboard
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();
        await expect(page.getByText("echo-task").first()).toBeVisible();
    });
});
