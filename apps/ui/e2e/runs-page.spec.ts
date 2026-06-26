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
        // The page is now chrome-less (matching a task's detail page): its title
        // lives in the topbar breadcrumb and the run rail carries a "Runs" label.
        await expect(page.getByRole("main").getByText("Runs", { exact: true })).toBeVisible();

        // Scope to <main>: the sidebar <aside> always lists every task by name,
        // so only the run rows in the main content reflect the active filter.
        // Use toHaveCount(0) (not toBeHidden) so the assertion retries cleanly
        // through the server-side refetch instead of tripping strict mode while
        // several rows are still present.
        const rows = page.getByRole("main");
        await expect(rows.getByText("echo-task").first()).toBeVisible();

        // Status now lives behind the Filter popover as outcome buckets.
        // Open it once; native controls keep it open as we toggle buckets.
        await page.getByTitle("Filter runs").click();

        // Failed only: fail-task remains, the (always-successful) echo-task drops.
        // The Failed bucket maps to the failure end-reasons; the individual
        // statuses live (collapsed) behind "Advanced", so this is unambiguous.
        await page.getByRole("checkbox", { name: "Failed", exact: true }).check();
        await expect(rows.getByText("fail-task").first()).toBeVisible();
        await expect(rows.getByText("echo-task")).toHaveCount(0, { timeout: 10_000 });

        // Succeeded only: clear Failed, select Succeeded — the inverse.
        await page.getByRole("checkbox", { name: "Failed", exact: true }).uncheck();
        await page.getByRole("checkbox", { name: "Succeeded", exact: true }).check();
        await expect(rows.getByText("echo-task").first()).toBeVisible();
        await expect(rows.getByText("fail-task")).toHaveCount(0, { timeout: 10_000 });
    });

    test("date range filter narrows the list to the chosen bound", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await seedEndedRun(page, "echo-task", daemonState.token);

        await page.goto("/runs");
        const rows = page.getByRole("main");
        await expect(rows.getByText("echo-task").first()).toBeVisible();

        await page.getByTitle("Filter runs").click();

        // A "To" bound in the distant past (everything up to end of that day)
        // excludes every run created today. The "From" end stays open.
        // exact:true: getByLabel does substring matching, and "Stopped" /
        // "Daemon stopped" / "Toggle line wrapping" all contain "to".
        await page.getByLabel("To", { exact: true }).fill("2016-07-10");
        await expect(rows.getByText("echo-task")).toHaveCount(0, { timeout: 10_000 });

        // Clearing the time chip brings the run back.
        await page.getByRole("button", { name: /Remove .* filter/ }).click();
        await expect(rows.getByText("echo-task").first()).toBeVisible();
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

        // The search moved to the app header; 250ms debounce, then a
        // server-side refetch filtered by task name.
        await page.getByPlaceholder("Search runs by task or ID…").fill("fail-task");
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

    test("an execution is linkable on the cross-task view", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        // Older run on a different task, then a newer one so the default
        // (newest) selection differs from the deep-linked target.
        const target = await seedEndedRun(page, "fail-task", daemonState.token);
        const newer = await seedEndedRun(page, "echo-task", daemonState.token);

        // The task-name-agnostic endpoint that lets the runs view restore a
        // deep-linked run that isn't on the loaded page.
        const byId = await page.request.get(`/api/runs/${target.id}`, {
            headers: { Authorization: `Bearer ${daemonState.token}` },
        });
        expect(byId.status(), "GET /api/runs/{runId}").toBe(200);
        expect(((await byId.json()) as { id: string }).id).toBe(target.id);

        // Deep link to the OLDER run via its path segment; the runs view restores
        // it (the run-id chip shows its full ULID) instead of defaulting to newest.
        await page.goto(`/runs/${target.id}`);
        await expect(page.getByText(target.id)).toBeVisible({ timeout: 10_000 });

        // Selecting the newest run from its row pushes it into the URL path.
        await page
            .getByRole("main")
            .getByRole("button")
            .filter({ hasText: "echo-task" })
            .first()
            .click();
        await expect(page).toHaveURL(new RegExp(`/runs/${newer.id}`));
        await expect(page.getByText(newer.id)).toBeVisible();

        // Reloading the synced URL restores that run.
        await page.reload();
        await expect(page).toHaveURL(new RegExp(`/runs/${newer.id}`));
        await expect(page.getByText(newer.id)).toBeVisible({ timeout: 10_000 });
    });
});
