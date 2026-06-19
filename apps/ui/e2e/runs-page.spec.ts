// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Page } from "@playwright/test";
import { test, expect } from "./fixtures/test-base";
import { runIsDeleted, triggerRunViaAPI, waitForRunEnded } from "./fixtures/api";

// The /runs history page had zero e2e coverage. These drive the real filter,
// search, and bulk-delete paths and verify the *backend* effect (via the API),
// not just the DOM. The list is virtualized (only visible rows are in the DOM)
// and the row variant shows no status word, so assertions key off task identity
// and the authoritative run record.
test.describe("runs page", () => {
    test.setTimeout(30_000);

    async function seedEndedRun(page: Page, task: string, token: string) {
        const run = await triggerRunViaAPI(page, task, token);
        return waitForRunEnded(page, task, run.id, token);
    }

    test("status filter narrows the list to matching runs", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await seedEndedRun(page, "echo-task", daemonState.token);
        await seedEndedRun(page, "fail-task", daemonState.token);

        await page.goto("/runs");
        await expect(page.getByRole("heading", { name: "Run History" })).toBeVisible();

        // Scope to <main>: the sidebar <aside> always lists every task by name,
        // so only the run rows in the main content reflect the active filter.
        // Use toHaveCount(0) (not toBeHidden) so the assertion retries cleanly
        // through the server-side refetch instead of tripping strict mode while
        // several rows are still present.
        const rows = page.getByRole("main");
        await expect(rows.getByText("echo-task").first()).toBeVisible();

        // Failed only: fail-task remains, the (always-successful) echo-task drops.
        await page.locator("select").selectOption("failed");
        await expect(rows.getByText("fail-task").first()).toBeVisible();
        await expect(rows.getByText("echo-task")).toHaveCount(0, { timeout: 10_000 });

        // Success only: the inverse.
        await page.locator("select").selectOption("success");
        await expect(rows.getByText("echo-task").first()).toBeVisible();
        await expect(rows.getByText("fail-task")).toHaveCount(0, { timeout: 10_000 });
    });

    test("search narrows the list to the typed task", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await seedEndedRun(page, "echo-task", daemonState.token);
        await seedEndedRun(page, "fail-task", daemonState.token);

        await page.goto("/runs");
        const rows = page.getByRole("main");
        await expect(rows.getByText("echo-task").first()).toBeVisible();
        await expect(rows.getByText("fail-task").first()).toBeVisible();

        // 250ms debounce, then a server-side refetch filtered by task name.
        await page.getByPlaceholder("Search task or ID...").fill("fail-task");
        await expect(rows.getByText("fail-task").first()).toBeVisible();
        await expect(rows.getByText("echo-task")).toHaveCount(0, { timeout: 10_000 });
    });

    test("bulk delete removes the run from the backend, not just the DOM", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        // Seed last so this run is newest → the first (top) row under the
        // default start_at-desc sort.
        const target = await seedEndedRun(page, "echo-task", daemonState.token);

        await page.goto("/runs");
        await expect(page.getByRole("main").getByText("echo-task").first()).toBeVisible();

        // Select the top row and delete it.
        await page
            .getByRole("checkbox", { name: /Select run from/ })
            .first()
            .check();
        await page.getByTitle(/^Delete runs?$/).click();

        await expect(page.getByText("Run deleted")).toBeVisible({ timeout: 10_000 });

        // The authoritative check: the daemon no longer has the run.
        await expect
            .poll(() => runIsDeleted(page, "echo-task", target.id, daemonState.token), {
                timeout: 10_000,
                message: "run should be deleted in the backend",
            })
            .toBe(true);
    });
});
