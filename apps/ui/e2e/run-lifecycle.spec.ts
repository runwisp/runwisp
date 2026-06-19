// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { test, expect } from "./fixtures/test-base";
import { expectRunDetailMatchesApi, getLatestRun, waitForRunEnded } from "./fixtures/api";

// Two complementary live-run paths (see fixtures/runwisp.e2e.toml):
//   - slow-task blocks until stopped, so the run stays in the "running" phase
//     deterministically until the test stops it — the operator-stop path, with
//     no timed sleep to race against.
//   - timed-task emits staggered output over ~6s then exits 0, so the test can
//     watch the natural RUNNING → SUCCESS transition, log lines arriving at the
//     right times, and the run row flipping status — all live, with no refresh.
//     The wall-clock here is the behaviour under test, not idle waiting.
test.describe("run lifecycle", () => {
    test.setTimeout(30_000);

    test("observe a live run, then stop it", async ({ authenticatedPage: page, daemonState }) => {
        await page.goto("/tasks/slow-task");
        await expect(page.getByRole("heading", { name: "slow-task", level: 1 })).toBeVisible();

        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        // The run-detail panel must show the live-running state: RUNNING badge,
        // the "Live" console indicator, and streamed output — all gated on the
        // run actually being in the running phase.
        await expect(page.getByText("RUNNING", { exact: true })).toBeVisible({ timeout: 30_000 });
        await expect(page.getByText("Live", { exact: true })).toBeVisible();
        await expect(page.getByText("slow-start")).toBeVisible({ timeout: 10_000 });

        const running = await getLatestRun(page, "slow-task", daemonState.token);
        expect(running, "slow-task should have a run").toBeDefined();
        if (!running) return;
        expect(running.status).toBe("running");

        // Stop it via the UI (Stop → confirm "Stop Now").
        await page.getByRole("button", { name: "Stop", exact: true }).click();
        await page.getByRole("button", { name: "Stop Now" }).click();

        // The daemon records an operator-initiated stop as end_reason "stopped".
        const ended = await waitForRunEnded(page, "slow-task", running.id, daemonState.token);
        expect(ended.end_reason).toBe("stopped");

        // The UI leaves the live state and the panel agrees with the record.
        await expect(page.getByText("STOPPED", { exact: true })).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("Live", { exact: true })).toBeHidden();
        await expectRunDetailMatchesApi(page, ended);
    });

    test("observe a live run finishing naturally, in detail and list", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/tasks/timed-task");
        await expect(page.getByRole("heading", { name: "timed-task", level: 1 })).toBeVisible();

        // Trigger via the UI (Run Task → confirm Run Now). The page opens the
        // detail panel on the new run AND lists its row, both fed by the same
        // live runs source — so both update via SSE without a page refresh.
        await page.getByRole("button", { name: "Run Task" }).click();
        await page.getByRole("button", { name: "Run Now" }).click();

        // The run-list row is a <button> in <main> whose text carries the
        // (lowercase) status word; the detail badge is an uppercase span, so this
        // never collides with the badge. The list is sorted newest-first and
        // timed-task is exercised only by this test, so the run we just triggered
        // is always the first matching row — .first() keeps us robust to runs
        // left over from a prior repeat/retry against the shared daemon.
        const listRow = (status: string) =>
            page.getByRole("main").getByRole("button").filter({ hasText: status });

        // --- Detail panel: the run is live and streaming ---
        await expect(page.getByText("RUNNING", { exact: true })).toBeVisible({ timeout: 15_000 });
        await expect(page.getByText("Live", { exact: true })).toBeVisible();
        await expect(page.getByText("timed-phase-1")).toBeVisible({ timeout: 10_000 });

        // Logs stream incrementally, not in one dump at the end: the final line
        // cannot be on screen yet while the run is in its first phase (it is not
        // echoed until ~6s in, so this has several seconds of margin).
        await expect(page.getByText("timed-done")).toHaveCount(0);

        // --- List: the row reflects the running run, live ---
        await expect(listRow("running").first()).toBeVisible({ timeout: 10_000 });

        // API ground truth mid-run; keep the id to await its natural end.
        const running = await getLatestRun(page, "timed-task", daemonState.token);
        expect(running, "timed-task should have a run").toBeDefined();
        if (!running) return;
        expect(running.status).toBe("running");

        // Later phases stream in over time, in order, while still running.
        await expect(page.getByText("timed-phase-2")).toBeVisible({ timeout: 10_000 });
        await expect(page.getByText("timed-phase-3")).toBeVisible({ timeout: 10_000 });

        // --- Natural finish, observed live (no reload/goto since the trigger) ---
        await expect(page.getByText("SUCCESS", { exact: true })).toBeVisible({ timeout: 15_000 });
        await expect(page.getByText("Live", { exact: true })).toBeHidden();
        await expect(page.getByText("timed-done")).toBeVisible();

        // The same row flips from running to success without a refresh: no row is
        // running any more, and the newest row (ours) now reads success.
        await expect(listRow("running")).toHaveCount(0, { timeout: 10_000 });
        await expect(listRow("success").first()).toBeVisible();

        // The record is a clean success and the panel matches it exactly.
        const ended = await waitForRunEnded(page, "timed-task", running.id, daemonState.token);
        expect(ended.end_reason).toBe("success");
        expect(ended.exit_code).toBe(0);
        await expectRunDetailMatchesApi(page, ended);
    });
});
