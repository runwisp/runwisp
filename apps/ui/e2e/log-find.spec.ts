// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import { runVerdict, triggerRunViaUI } from "./fixtures/api";

test.describe("find in a run's log", () => {
    test("Ctrl+F finds text in the open run and Enter steps through it", async ({
        authenticatedPage: page,
    }) => {
        await page.goto("/tasks/long-output-task");
        await triggerRunViaUI(page, "long-output-task");
        await expect(runVerdict(page, "succeeded")).toBeVisible({ timeout: 30_000 });
        const main = page.getByRole("main");
        await expect(main.getByText("long-line-400")).toBeVisible({ timeout: 10_000 });

        await page.locator("body").press("ControlOrMeta+f");
        const input = main.getByRole("textbox", { name: "Find in this log" });
        await expect(input).toBeFocused();

        // long-line-39 and long-line-390..399: 11 lines, most of them far
        // above the lines the console has on screen.
        await input.fill("long-line-39");
        const count = main.getByTestId("log-find-count");
        await expect(count).toHaveText(/^\d+ of 11$/);
        const first = await count.textContent();

        await input.press("Enter");
        await expect(count).not.toHaveText(first ?? "");
        await expect(main.locator(".log-line--current")).toContainText("long-line-39");

        await input.fill("no-such-line");
        await expect(count).toHaveText("No matches");

        await input.press("Escape");
        await expect(input).toHaveCount(0);
        await expect(main.locator(".log-line--current")).toHaveCount(0);
    });
});
