// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";
import {
    expectRunDetailMatchesApi,
    getLatestRun,
    triggerRunViaAPI,
    waitForRunEnded,
} from "./fixtures/api";

test.describe("task execution", () => {
    test("trigger successful task and verify completion", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger from the empty-state Run button (no runs yet).
        await page.getByRole("button", { name: /^Run( task)?$/ }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        // Wait for the run detail panel to settle on SUCCESS (exact match avoids
        // the lowercase "success" in the run list, which uses CSS capitalize).
        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // Cross-check the rendered outcome against the daemon's own record: the
        // panel must show the API's status, exit code, and real timing — not a
        // hardcoded badge.
        const apiRun = await getLatestRun(page, "echo-task", daemonState.token);
        expect(apiRun, "echo-task should have a run").toBeDefined();
        if (!apiRun) return;
        expect(apiRun.end_reason).toBe("success");
        expect(apiRun.exit_code).toBe(0);
        await expectRunDetailMatchesApi(page, apiRun);

        // All captured stdout is present, in order — not just the first line.
        await expect(page.getByText("echo-line-1")).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("echo-line-2")).toBeVisible();
        await expect(page.getByText("echo-line-3")).toBeVisible();
    });

    test("trigger failing task and verify failure status", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/tasks/fail-task");

        await expect(page.getByRole("heading", { name: "fail-task", level: 1 })).toBeVisible();

        // Trigger from the empty-state Run button (no runs yet).
        await page.getByRole("button", { name: /^Run( task)?$/ }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        await expect(page.getByText("FAILED", { exact: true })).toBeVisible({ timeout: 30_000 });

        const apiRun = await getLatestRun(page, "fail-task", daemonState.token);
        expect(apiRun, "fail-task should have a run").toBeDefined();
        if (!apiRun) return;
        expect(apiRun.end_reason).toBe("failed");
        expect(apiRun.exit_code).toBe(1);
        await expectRunDetailMatchesApi(page, apiRun);

        // Both stdout and stderr are captured and surfaced in the console.
        await expect(page.getByText("fail-line-1")).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("fail-line-2-stderr")).toBeVisible();
    });

    test("run appears in task run history", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger from the empty-state Run button (no runs yet).
        await page.getByRole("button", { name: /^Run( task)?$/ }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // The run detail panel surfaces how the run was triggered: a manually
        // triggered run reads as an "API" trigger.
        await expect(page.getByText("API").first()).toBeVisible();
    });

    test("dashboard activity feed updates after task run", async ({ authenticatedPage: page }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger from the empty-state Run button (no runs yet).
        await page.getByRole("button", { name: /^Run( task)?$/ }).click();
        await page.getByRole("button", { name: "Run Now" }).click();
        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // Navigate to dashboard
        await page.goto("/");
        await expect(page.getByRole("heading", { name: "Tasks" })).toBeVisible();

        await expect(page.getByRole("heading", { name: "Recent activity" })).toBeVisible();
        await expect(page.getByText("echo-task").first()).toBeVisible();
    });

    test("an execution is linkable: selecting a run syncs the URL and reloads restore it", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        // Two ended runs so there's an older run distinct from the default
        // (newest) selection. The run-id chip in the detail panel renders the
        // full ULID, so getByText(id) keys assertions to the selected run.
        const older = await triggerRunViaAPI(page, "echo-task", daemonState.token);
        await waitForRunEnded(page, "echo-task", older.id, daemonState.token);
        const newer = await triggerRunViaAPI(page, "echo-task", daemonState.token);
        await waitForRunEnded(page, "echo-task", newer.id, daemonState.token);

        // Deep link straight to the OLDER run via its path segment — it must
        // override the default newest-run selection (the read path).
        await page.goto(`/tasks/echo-task/${older.id}`);
        await expect(page.getByText(older.id)).toBeVisible({ timeout: 10_000 });

        // Selecting the newest run from its rail row must push it into the URL
        // path (the write path). Run rows are <button>s in <main> carrying the
        // lowercase status; .first() is the newest under the default sort.
        await page
            .getByRole("main")
            .getByRole("button")
            .filter({ hasText: "success" })
            .first()
            .click();
        await expect(page).toHaveURL(new RegExp(`/tasks/echo-task/${newer.id}`));
        await expect(page.getByText(newer.id)).toBeVisible();

        // Reloading the synced URL restores that run, not the default.
        await page.reload();
        await expect(page).toHaveURL(new RegExp(`/tasks/echo-task/${newer.id}`));
        await expect(page.getByText(newer.id)).toBeVisible({ timeout: 10_000 });
    });
});
