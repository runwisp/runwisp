// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import {
    expectRunDetailMatchesApi,
    runVerdict,
    triggerRunViaAPI,
    triggerRunViaUI,
    waitForRunEnded,
} from "./fixtures/api";

test.describe("task execution", () => {
    test("trigger successful task and verify completion", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger via the Run button and capture the run it created. The panel
        // may already show a prior run's SUCCESS badge (the daemon is shared
        // across specs), so we anchor on *this* run's id rather than "the latest".
        const triggered = await triggerRunViaUI(page, "echo-task");

        // Poll the API for this run's terminal record before asserting on it —
        // this is the source of truth for the outcome, exit code, and timing.
        const apiRun = await waitForRunEnded(page, "echo-task", triggered.id, daemonState.token);
        expect(apiRun.endReason).toBe("succeeded");
        expect(apiRun.exitCode).toBe(0);

        // The panel settles on SUCCESS (exact match avoids the lowercase
        // "succeeded" in the run list, which uses CSS capitalize) and must show
        // the API's status, exit code, and real timing — not a hardcoded badge.
        await expect(runVerdict(page, "succeeded")).toBeVisible({ timeout: 30_000 });
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

        // Trigger via the Run button and anchor on the run it created — a prior
        // fail-task run's FAILED badge may already be on screen (shared daemon),
        // so asserting on "the latest run" would race the run we just triggered.
        const triggered = await triggerRunViaUI(page, "fail-task");

        const apiRun = await waitForRunEnded(page, "fail-task", triggered.id, daemonState.token);
        expect(apiRun.endReason).toBe("failed");
        expect(apiRun.exitCode).toBe(1);

        await expect(runVerdict(page, "failed")).toBeVisible({ timeout: 30_000 });
        await expectRunDetailMatchesApi(page, apiRun);

        // Both stdout and stderr are captured and surfaced in the console.
        await expect(page.getByText("fail-line-1")).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("fail-line-2-stderr")).toBeVisible();
    });

    test("run confirmation modal traps focus and restores it on close", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        const runButton = page.getByRole("button", { name: /^Run( task)?$/ });
        await runButton.click();

        const dialog = page.getByRole("dialog");
        await expect(dialog).toBeVisible();

        // Opening the dialog must move focus inside it (the "Close" button is
        // the first focusable element) rather than leaving it on the trigger
        // or the page behind the overlay.
        const closeButton = dialog.getByRole("button", { name: "Close" });
        await expect(closeButton).toBeFocused();

        // Shift+Tab from the first focusable element must wrap to the last
        // ("Run Now") — proving Tab is trapped inside the dialog instead of
        // escaping to whatever the portaled dialog happens to sit next to in
        // the DOM.
        await page.keyboard.press("Shift+Tab");
        await expect(dialog.getByRole("button", { name: "Run Now" })).toBeFocused();

        // Tab forward from the last element wraps back to the first, and at
        // every step focus stays inside the dialog (never on the page behind).
        await page.keyboard.press("Tab");
        await expect(closeButton).toBeFocused();
        await expect(dialog.locator(":focus")).toHaveCount(1);

        // Escape closes the dialog and restores focus to the button that
        // opened it, rather than dropping focus to the document body.
        await page.keyboard.press("Escape");
        await expect(dialog).toBeHidden();
        await expect(runButton).toBeFocused();
    });

    test("run appears in task run history", async ({ authenticatedPage: page, daemonState }) => {
        await page.goto("/tasks/echo-task");

        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger via the Run button and wait for this run to finish before
        // asserting — the panel may otherwise show a prior run's badge.
        const triggered = await triggerRunViaUI(page, "echo-task");
        await waitForRunEnded(page, "echo-task", triggered.id, daemonState.token);

        await expect(runVerdict(page, "succeeded")).toBeVisible({ timeout: 30_000 });

        // The run detail panel surfaces how the run was triggered: a manually
        // triggered run reads as an "API" trigger.
        await expect(page.getByText("API").first()).toBeVisible();
    });

    test("dashboard activity feed updates after task run", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/tasks/echo-task");
        await expect(page.getByRole("heading", { name: "echo-task", level: 1 })).toBeVisible();

        // Trigger via the Run button and wait for it to finish, so the activity
        // feed has a completed run to surface.
        const triggered = await triggerRunViaUI(page, "echo-task");
        await waitForRunEnded(page, "echo-task", triggered.id, daemonState.token);
        await expect(runVerdict(page, "succeeded")).toBeVisible({ timeout: 30_000 });

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
            .filter({ hasText: "succeeded" })
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
