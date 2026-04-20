// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";

test.describe("authentication", () => {
    test("shows auth modal when unauthenticated", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await expect(page.getByText("Authentication Required")).toBeVisible();
        await expect(page.getByLabel("Password")).toBeVisible();
        await expect(page.getByRole("button", { name: "Login" })).toBeVisible();

        await context.close();
    });

    test("login with correct password grants access", async ({ browser, daemonState }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByText("Authentication Required").waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill(daemonState.password);
        await page.getByRole("button", { name: "Login" }).click();

        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();

        await context.close();
    });

    test("login with wrong password shows error", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByText("Authentication Required").waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill("wrong-password-12345");
        await page.getByRole("button", { name: "Login" }).click();

        await expect(page.getByText("Invalid password")).toBeVisible();
        await expect(page.getByText("Authentication Required")).toBeVisible();

        await context.close();
    });
});
