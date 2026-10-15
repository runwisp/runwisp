// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";

test.describe("authentication", () => {
    test("shows auth modal when unauthenticated", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await expect(page.getByRole("dialog", { name: "RunWisp" })).toBeVisible();
        const passwordField = page.getByLabel("Password");
        await expect(passwordField).toBeVisible();
        await expect(page.getByRole("button", { name: "Login" })).toBeVisible();

        // Password-manager save/autofill hooks: a hidden username field paired
        // with autocomplete hints so Firefox, Chrome, Bitwarden, and 1Password
        // recognize this as a login form (see AuthModal.svelte).
        await expect(passwordField).toHaveAttribute("name", "password");
        await expect(passwordField).toHaveAttribute("autocomplete", "current-password");
        await expect(page.locator('input[autocomplete="username"]')).toHaveAttribute(
            "name",
            "username",
        );

        await context.close();
    });

    test("login with correct password grants access", async ({ browser, daemonState }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByRole("dialog", { name: "RunWisp" }).waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill(daemonState.password);
        await page.getByRole("button", { name: "Login" }).click();

        await expect(page.getByRole("heading", { name: "System resources" })).toBeVisible();

        await context.close();
    });

    test("login with wrong password is rejected", async ({ browser }) => {
        const context = await browser.newContext({ bypassCSP: true });
        const page = await context.newPage();

        await page.goto("/");
        await page.getByRole("dialog", { name: "RunWisp" }).waitFor({ timeout: 15_000 });
        await page.getByLabel("Password").fill("wrong-password-12345");
        await page.getByRole("button", { name: "Login" }).click();

        // Access is denied and the modal stays open with an error. Only failed
        // logins count toward the lockout, so the run's earlier successful
        // logins can't turn this into the rate-limit notice.
        await expect(page.getByText("Invalid password. Please try again.")).toBeVisible();
        await expect(page.getByRole("dialog", { name: "RunWisp" })).toBeVisible();

        await context.close();
    });
});
