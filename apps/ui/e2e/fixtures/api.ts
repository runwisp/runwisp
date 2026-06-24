// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { expect, type Page } from "@playwright/test";
import { displayStatus, type Run } from "@runwisp/common";

/**
 * Helpers that talk to the daemon's REST API directly (Playwright's
 * `page.request`, carrying the JWT from the `daemonState` fixture).
 *
 * The daemon API is the source of truth — the UI is "read-only + trigger", so
 * these helpers let a spec establish *what actually happened* and then assert
 * the UI reflects it. Use them to seed runs deterministically and to
 * cross-check rendered values against the authoritative run record.
 */

function authHeaders(token: string): Record<string, string> {
    return { Authorization: `Bearer ${token}` };
}

/** Trigger a run via `POST /api/tasks/{name}/run`; returns the created run. */
export async function triggerRunViaAPI(page: Page, taskName: string, token: string): Promise<Run> {
    const response = await page.request.post(`/api/tasks/${taskName}/run`, {
        headers: authHeaders(token),
    });
    expect(response.status(), `trigger ${taskName}`).toBeLessThan(400);
    return (await response.json()) as Run;
}

/** Fetch a single run via `GET /api/tasks/{name}/runs/{id}`. */
export async function getRun(
    page: Page,
    taskName: string,
    runId: string,
    token: string,
): Promise<Run> {
    const response = await page.request.get(`/api/tasks/${taskName}/runs/${runId}`, {
        headers: authHeaders(token),
    });
    expect(response.status(), `get run ${runId}`).toBe(200);
    return (await response.json()) as Run;
}

/** Newest run for a task, or undefined if it has none. */
export async function getLatestRun(
    page: Page,
    taskName: string,
    token: string,
): Promise<Run | undefined> {
    const response = await page.request.get(`/api/tasks/${taskName}/runs?limit=1`, {
        headers: authHeaders(token),
    });
    expect(response.status(), `list runs for ${taskName}`).toBe(200);
    const body = (await response.json()) as { runs: Run[] };
    return body.runs[0];
}

/** Poll the API until the run reaches the terminal `ended` phase. */
export async function waitForRunEnded(
    page: Page,
    taskName: string,
    runId: string,
    token: string,
    timeout = 20_000,
): Promise<Run> {
    let latest: Run | undefined;
    await expect
        .poll(
            async () => {
                latest = await getRun(page, taskName, runId, token);
                return latest.status;
            },
            { timeout, message: `run ${runId} should reach ended` },
        )
        .toBe("ended");
    if (!latest) throw new Error(`run ${runId} never observed`);
    return latest;
}

/** Returns true once the run is gone (deleted) — `GET` answers 404. */
export async function runIsDeleted(
    page: Page,
    taskName: string,
    runId: string,
    token: string,
): Promise<boolean> {
    const response = await page.request.get(`/api/tasks/${taskName}/runs/${runId}`, {
        headers: authHeaders(token),
    });
    return response.status() === 404;
}

/**
 * Mark all notifications read via the API. Clears the unread badge before a
 * spec runs, since the shared single-worker daemon carries state across specs.
 */
export async function markAllReadViaAPI(page: Page, token: string): Promise<void> {
    const response = await page.request.post("/api/notifications/read", {
        headers: authHeaders(token),
    });
    expect(response.status(), "mark all read").toBeLessThan(400);
}

/**
 * Poll `GET /api/notifications/unread-count` until at least one notification is
 * unread (backend truth that the in-app notification has been persisted).
 *
 * The bell badge only updates via a forward-only SSE stream with no replay, so
 * a notification raised in the window around the page's stream subscription can
 * be lost to that client forever. Specs that assert the badge should confirm
 * the backend state here and then `page.reload()` so the badge renders from the
 * deterministic initial fetch instead of racing the live push.
 */
export async function waitForUnreadNotification(
    page: Page,
    token: string,
    timeout = 20_000,
): Promise<void> {
    await expect
        .poll(
            async () => {
                const response = await page.request.get("/api/notifications/unread-count", {
                    headers: authHeaders(token),
                });
                if (!response.ok()) return 0;
                const body = (await response.json()) as { count: number };
                return body.count;
            },
            { timeout, message: "an unread notification should be persisted" },
        )
        .toBeGreaterThan(0);
}

/**
 * Poll `GET /api/notifications` until `taskName`'s coalesced row reaches
 * `minCount` occurrences — the deterministic signal that repeated failures have
 * merged, used before asserting the coalesced "N×" row in the UI.
 */
export async function waitForCoalescedCount(
    page: Page,
    taskName: string,
    minCount: number,
    token: string,
    timeout = 20_000,
): Promise<void> {
    await expect
        .poll(
            async () => {
                const response = await page.request.get("/api/notifications?limit=50", {
                    headers: authHeaders(token),
                });
                if (!response.ok()) return 0;
                const body = (await response.json()) as {
                    items: { task_name: string; count: number }[];
                };
                const row = body.items.find((item) => item.task_name === taskName);
                return row ? row.count : 0;
            },
            { timeout, message: `${taskName} should coalesce to >= ${minCount} occurrences` },
        )
        .toBeGreaterThanOrEqual(minCount);
}

/**
 * Assert the open run-detail panel shows the same outcome the API recorded.
 * This catches the UI silently rendering a wrong/stale status, exit code, or
 * missing timing — the "nothing silently fails" guarantee, verified end-to-end.
 */
export async function expectRunDetailMatchesApi(page: Page, apiRun: Run): Promise<void> {
    const status = displayStatus(apiRun.status, apiRun.end_reason);
    await expect(
        page.getByText(status.toUpperCase(), { exact: true }),
        `badge should show ${status.toUpperCase()}`,
    ).toBeVisible();

    // Only a naturally-finished run (success/failed) carries a real process
    // exit code; stopped/timed-out/crashed runs end on a synthetic sentinel, so
    // the panel shows "—" plus a reason instead of a misleading "Code -1".
    if (apiRun.end_reason === "success" || apiRun.end_reason === "failed") {
        // The Exit metadata cell shows the bare process exit code (with a
        // "fail" badge on non-zero); the value span follows the "Exit" label.
        const exitValue = page
            .getByText("Exit", { exact: true })
            .locator("xpath=following-sibling::span[1]");
        await expect(exitValue, `exit code should be ${apiRun.exit_code}`).toContainText(
            String(apiRun.exit_code),
        );
    }

    // The metadata value span sits immediately after its label span.
    const startedValue = page
        .getByText("Started", { exact: true })
        .locator("xpath=following-sibling::span[1]");
    const durationValue = page
        .getByText("Ran for", { exact: true })
        .locator("xpath=following-sibling::span[1]");

    if (apiRun.start_at) {
        await expect(startedValue, "started timestamp populated").not.toHaveText("—");
        await expect(startedValue).toContainText(":"); // wall-clock time (HH:MM:SS)
    }
    if (apiRun.start_at && apiRun.end_at) {
        await expect(durationValue, "duration populated").not.toHaveText("—");
    }
}
