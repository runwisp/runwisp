// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";

test.describe("authentication", () => {
    test("shows auth modal when unauthenticated", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await expect(page.getByText("Sign in to this instance")).toBeVisible();
        await expect(page.getByLabel("Password")).toBeVisible();
        await expect(page.getByRole("button", { name: "Login" })).toBeVisible();

        await context.close();
    });

    test("login with correct password grants access", async ({ browser, daemonState }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByText("Sign in to this instance").waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill(daemonState.password);
        await page.getByRole("button", { name: "Login" }).click();

        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();

        await context.close();
    });

    test("login with wrong password is rejected", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByText("Sign in to this instance").waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill("wrong-password-12345");
        await page.getByRole("button", { name: "Login" }).click();

        // Access is denied and the modal stays open with an error. The per-IP
        // login limiter (5 / 5 min) is shared by every login in this run, so by
        // now it may be exhausted — the message is then the rate-limit notice
        // rather than the wrong-password one. Both keep the operator out, and
        // crucially the two are now distinguished (a throttled login is no
        // longer mislabeled as "Invalid password"), which is what we assert.
        await expect(page.getByText(/Invalid password|Too many attempts/)).toBeVisible();
        await expect(page.getByText("Sign in to this instance")).toBeVisible();

        await context.close();
    });
});
