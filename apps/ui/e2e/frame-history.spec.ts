// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";

test.describe("frame history", () => {
    test("settled progress bar exposes rewindable frames inline", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/progress-task");
        await expect(page.getByRole("heading", { name: "progress-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 30_000 });

        // Reload so the run is loaded fresh from disk (backfill only, no live
        // events) — the path where frame history used to be lost on a finished run.
        await page.reload();
        await expect(page.getByRole("heading", { name: "progress-task", level: 1 })).toBeVisible();

        const main = page.getByRole("main");

        // The bar collapsed to a single committed final frame in the console.
        await expect(main.getByText("progress: 100%")).toBeVisible({ timeout: 10_000 });

        // The committed line advertises frame history with a rewind affordance.
        const toggle = main.getByRole("button", { name: /Toggle frame history/ });
        await expect(toggle).toBeVisible({ timeout: 10_000 });

        // Expanding it reveals the prior frames the bar animated through, whole.
        await toggle.click();
        await expect(main.getByText(/Frame 1 of/)).toBeVisible({ timeout: 10_000 });
        // An earlier (non-final) frame is present in the unfolded history.
        await expect(main.getByText(/progress: (10|40|70)%/).first()).toBeVisible();

        // Re-clicking collapses the history block.
        await toggle.click();
        await expect(main.getByText(/Frame 1 of/)).toHaveCount(0);
    });
});
