// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import { runVerdict, triggerRunViaUI } from "./fixtures/api";

const LONG = "long-output-task";

test.describe("task strip", () => {
    test("a failed last run shows on the badge, which opens it", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/fail-task");
        const run = await triggerRunViaUI(page, "fail-task");
        await expect(runVerdict(page, "failed")).toBeVisible({ timeout: 30_000 });

        const badge = page.getByTestId("task-strip").getByTestId("task-state");
        await expect(badge).toHaveText("Last run failed", { timeout: 10_000 });
        await page.goto("/tasks/fail-task");
        await badge.click();
        await expect(page).toHaveURL(new RegExp(`/tasks/fail-task/${run.id}`));
    });

    test("Details shows the run command and env in runwisp.toml terms", async ({
        authenticatedPage: page,
    }) => {
        await page.goto(`/tasks/${LONG}`);
        await page.getByTestId("task-strip").getByRole("button", { name: "Details" }).click();
        const details = page.getByTestId("task-details");
        await expect(details.getByTestId("task-command")).toContainText("seq 1 400");
        await expect(details.getByText("env.BUCKET")).toBeVisible();
        await expect(details.getByText('"acme"')).toBeVisible();
    });

    test("scrolling the log folds the strip into the top bar and back", async ({
        authenticatedPage: page,
    }) => {
        await page.setViewportSize({ width: 1280, height: 640 });
        await page.goto(`/tasks/${LONG}`);
        await triggerRunViaUI(page, LONG);
        await expect(runVerdict(page, "succeeded")).toBeVisible({ timeout: 30_000 });

        const strip = page.getByTestId("task-strip");
        const bar = page.getByTestId("task-bar");
        const log = page.locator("[data-content-height]");
        await expect(log).toContainText("long-line-400");
        await expect(strip).toBeVisible();
        await expect(bar).toHaveCount(0);

        // Away from the end and back down to it: reaching the bottom folds.
        await log.hover();
        await page.mouse.wheel(0, -300);
        await expect(strip).toBeVisible();
        await page.mouse.wheel(0, 300);
        await expect(bar).toBeVisible();
        await expect(strip).toHaveCount(0);
        await expect(bar.getByTestId("task-state")).toHaveText("Manual only");

        // 240px up without reversing unfolds. Scrolls while the fold still
        // animates don't count, so retry until one lands after it.
        await expect(async () => {
            await page.mouse.wheel(0, -300);
            await expect(strip).toBeVisible({ timeout: 500 });
        }).toPass();
        await expect(bar).toHaveCount(0);
    });

    for (const width of [800, 1280]) {
        test(`the strip stays on one line at ${String(width)}×640`, async ({
            authenticatedPage: page,
        }) => {
            await page.setViewportSize({ width, height: 640 });
            await page.goto("/tasks/nightly-task");
            const row = page.getByTestId("task-strip-row");
            await expect(row.getByTestId("task-state")).toBeVisible();
            const fits = await row.evaluate(
                (el) => el.scrollWidth <= el.clientWidth && el.clientHeight < 48,
            );
            expect(fits).toBe(true);
        });
    }

    test("on a phone, a task with no runs still has its strip and Run action", async ({
        authenticatedPage: page,
    }) => {
        await page.setViewportSize({ width: 390, height: 844 });
        await page.goto("/tasks/bare-task");

        const strip = page.getByTestId("task-strip");
        await expect(strip).toBeVisible();
        await expect(strip.getByRole("button", { name: "Run now" })).toBeVisible();
        await expect(strip.getByRole("button", { name: "Details" })).toBeVisible();
    });

    test("Details hides keys left at their defaults and shows an explicit zero", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/bare-task");
        await page.getByTestId("task-strip").getByRole("button", { name: "Details" }).click();
        const details = page.getByTestId("task-details");
        await expect(details.getByTestId("task-command")).toContainText("true");

        // keep_runs = 0 is set in the TOML, so it shows with its zero.
        await expect(details.getByText("keep_runs", { exact: true })).toBeVisible();
        await expect(details.getByText("0", { exact: true })).toBeVisible();

        // The API serves resolved defaults; none of them is listed.
        for (const key of [
            "graceful_stop",
            "stop_signal",
            "shell",
            "max_concurrent",
            "max_queued",
        ]) {
            await expect(details.getByText(key, { exact: true })).toHaveCount(0);
        }
        await expect(details.getByText("Execution", { exact: true })).toHaveCount(0);
    });

    test("a service that exited under restart = never offers Start, not restarting", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        const service = "oneshot-service";
        await page.goto(`/tasks/${service}`);
        const strip = page.getByTestId("task-strip");
        await strip.getByRole("button", { name: "Start", exact: true }).click();
        await page.getByRole("dialog").getByRole("button", { name: "Start Now" }).click();

        // A clean exit with no restart coming reads as stopped, not broken.
        await expect(strip).toContainText("Stopped", { timeout: 15_000 });
        await expect(strip).toContainText("Stays down until you start it");
        await expect(strip).not.toContainText(/restarting/i);
        await expect(strip.getByRole("button", { name: "Start", exact: true })).toBeVisible();

        // The API agrees: stopped, not restarting.
        const res = await page.request.get(`/api/tasks/${service}`, {
            headers: { Authorization: `Bearer ${daemonState.token}` },
        });
        expect(JSON.stringify(await res.json())).not.toContain("restarting");
    });
});
