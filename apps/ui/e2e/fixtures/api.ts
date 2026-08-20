// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { expect, type Locator, type Page } from "@playwright/test";
import { displayStatus, type Run, type RunStatus } from "@runwisp/common";

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

/**
 * The run-detail verdict line, optionally narrowed to one outcome. The panel
 * states its outcome as a phrase ("succeeded in 933ms"), so specs match on the
 * status the element carries rather than on the rendered wording — the phrasing
 * is presentation and free to change, the status is the contract.
 */
export function runVerdict(page: Page, status?: RunStatus): Locator {
    const selector = status
        ? `[data-testid="run-verdict"][data-status="${status}"]`
        : `[data-testid="run-verdict"]`;
    return page.locator(selector);
}

/** Trigger a run via `POST /api/tasks/{name}/run`; returns the created run. */
export async function triggerRunViaAPI(page: Page, taskName: string, token: string): Promise<Run> {
    const response = await page.request.post(`/api/tasks/${taskName}/run`, {
        headers: authHeaders(token),
    });
    expect(response.status(), `trigger ${taskName}`).toBeLessThan(400);
    return (await response.json()) as Run;
}

/**
 * Trigger a run through the UI — the "Run" button then the "Run Now" confirm —
 * and return the run the daemon created, captured from the trigger POST.
 *
 * Anchoring later assertions to this run id is what makes them reliable. The
 * run-detail panel defaults to the newest *existing* run on load, and the
 * single shared daemon carries runs across specs, so "wait for a SUCCESS/FAILED
 * badge" on its own can latch onto a prior run's badge while the run we just
 * triggered is still in flight — the API's newest run would then still be
 * mid-run (`endReason` unset). Reading the created run's id here removes that
 * race: we poll for *this* run's terminal state, not "the latest".
 */
export async function triggerRunViaUI(page: Page, taskName: string): Promise<Run> {
    // Attach the response listener before the click that fires the POST. Match
    // the exact path so the runs *list* (`…/runs`) can't be mistaken for the
    // trigger (`…/run`).
    const triggered = page.waitForResponse(
        (response) =>
            response.request().method() === "POST" &&
            new URL(response.url()).pathname === `/api/tasks/${taskName}/run`,
    );
    await page.getByRole("button", { name: /^Run( task)?$/ }).click();
    await page.getByRole("button", { name: "Run Now" }).click();
    const response = await triggered;
    expect(response.status(), `trigger ${taskName} via UI`).toBeLessThan(400);
    return (await response.json()) as Run;
}

/** Fetch a single run via `GET /api/runs/{id}`. Run ULIDs are globally unique,
 * so `taskName` is kept only to avoid churning the callers that pass it. */
export async function getRun(
    page: Page,
    _taskName: string,
    runId: string,
    token: string,
): Promise<Run> {
    const response = await page.request.get(`/api/runs/${runId}`, {
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
    const body = (await response.json()) as { items: Run[] };
    return body.items[0];
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
    _taskName: string,
    runId: string,
    token: string,
): Promise<boolean> {
    const response = await page.request.get(`/api/runs/${runId}`, {
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
 * Poll `GET /api/notifications/unreadCount` until at least one notification is
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
                const response = await page.request.get("/api/notifications/unreadCount", {
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
                    items: { taskName: string; count: number }[];
                };
                const row = body.items.find((item) => item.taskName === taskName);
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
    const status = displayStatus(apiRun.status, apiRun.endReason);
    await expect(runVerdict(page), `verdict should report ${status}`).toHaveAttribute(
        "data-status",
        status,
    );

    // The verdict shows a code only when it is news. A failure's code is the
    // first thing to triage on and must match the record exactly; a success is
    // always exit 0 (the phrase already says so), and stopped/timed-out/crashed
    // runs end on a synthetic sentinel that would render a misleading "-1" — so
    // both must show no code at all rather than a wrong or redundant one.
    if (apiRun.endReason === "failed") {
        await expect(
            page.getByTestId("run-exit"),
            `exit code should be ${apiRun.exitCode}`,
        ).toContainText(String(apiRun.exitCode));
    } else {
        await expect(page.getByTestId("run-exit"), "no code unless it is news").toHaveCount(0);
    }

    if (apiRun.startedAt) {
        const startedValue = page.getByTestId("run-started");
        await expect(startedValue, "started timestamp populated").not.toHaveText("—");
        await expect(startedValue).toContainText(":"); // wall-clock time (HH:MM:SS)
    }
    if (apiRun.startedAt && apiRun.endedAt) {
        await expect(page.getByTestId("run-duration"), "duration populated").toBeVisible();
    }
}
