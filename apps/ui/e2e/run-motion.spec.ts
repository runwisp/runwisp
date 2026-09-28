// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { test, expect } from "./fixtures/test-base";
import { triggerRunViaAPI, waitForRunEnded } from "./fixtures/api";

// A run that arrives live (API trigger, standing in for a scheduled firing)
// slides into the history with the `.run-arrive` cue, while rows already on
// screen keep their elements and slide aside. The class is never removed, so
// asserting on it doesn't race the animation.
const TASK = "arrival-task";
const rows = '[class~="group/row"]';

test.describe("run arrival and removal animation", () => {
    test("a live run slides in without remounting the rows below it", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        for (let i = 0; i < 3; i++) {
            const run = await triggerRunViaAPI(page, TASK, daemonState.token);
            await waitForRunEnded(page, TASK, run.id, daemonState.token);
        }
        await page.goto(`/tasks/${TASK}`);
        await expect(page.locator(rows).nth(2)).toBeVisible();
        const before = await page.locator(rows).count();

        // Tag the rows on screen. Regression: the bottom row used to unmount
        // for a frame and pop back in instead of sliding.
        await page.evaluate((sel) => {
            const seen = [...document.querySelectorAll(sel)];
            Object.assign(window, { __seen: seen });
        }, rows);

        await triggerRunViaAPI(page, TASK, daemonState.token);
        await expect(page.locator(rows)).toHaveCount(before + 1);

        const result = await page.evaluate((sel) => {
            const seen: Element[] = Reflect.get(window, "__seen");
            const fresh = [...document.querySelectorAll(sel)].filter((el) => !seen.includes(el));
            return {
                allKept: seen.every((el) => el.isConnected),
                freshArrives: fresh.length === 1 && fresh[0].classList.contains("run-arrive"),
                oldArrive: seen.some((el) => el.classList.contains("run-arrive")),
            };
        }, rows);
        expect(result).toEqual({ allKept: true, freshArrives: true, oldArrive: false });
    });

    test("a removed run sweeps out while the rows below close the gap", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        const runs = [];
        for (let i = 0; i < 3; i++) {
            const run = await triggerRunViaAPI(page, TASK, daemonState.token);
            runs.push(await waitForRunEnded(page, TASK, run.id, daemonState.token));
        }
        const newest = runs[2];
        await page.goto(`/tasks/${TASK}`);
        await expect(page.locator(`[data-run-id="${newest.id}"]`)).toBeVisible();

        // Sample every frame: the removed row must linger fading out, and the
        // row below must keep its element and slide up through in-between
        // positions rather than jumping.
        await page.evaluate((id) => {
            const gone = document.querySelector(`[data-run-id="${id}"]`);
            const below = gone?.nextElementSibling;
            const samples = { opacities: [] as number[], tops: [] as number[], belowKept: true };
            Object.assign(window, { __samples: samples });
            const tick = () => {
                if (gone?.isConnected)
                    samples.opacities.push(Number(getComputedStyle(gone).opacity));
                if (below) {
                    samples.belowKept &&= below.isConnected;
                    samples.tops.push(Math.round(below.getBoundingClientRect().top));
                }
                requestAnimationFrame(tick);
            };
            requestAnimationFrame(tick);
        }, newest.id);

        const deleted = await page.request.post("/api/runs/bulk/delete", {
            headers: { Authorization: `Bearer ${daemonState.token}` },
            data: { matchAll: false, ids: [newest.id] },
        });
        expect(deleted.status()).toBeLessThan(400);
        await expect(page.locator(`[data-run-id="${newest.id}"]`)).toHaveCount(0);
        await page.waitForTimeout(400); // let the gap finish closing

        const samples = await page.evaluate(() => Reflect.get(window, "__samples"));
        expect(samples.opacities.some((o: number) => o > 0 && o < 1)).toBe(true);
        expect(samples.belowKept).toBe(true);
        expect(new Set(samples.tops).size).toBeGreaterThan(2);
    });

    test("oldest-first: a live run arrives at the bottom", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto(`/tasks/${TASK}`);
        await expect(page.locator(rows).first()).toBeVisible();
        // Let the re-sorted list load before triggering: a run arriving while
        // that fetch is in flight is overwritten by the older snapshot.
        const resorted = page.waitForResponse((response) => {
            const url = new URL(response.url());
            return (
                url.pathname === "/api/runs" &&
                url.searchParams.get("taskName") === TASK &&
                url.searchParams.get("sortDirection") === "asc"
            );
        });
        await page.getByTitle("Toggle sort order").click();
        await resorted;
        await expect(page.locator(`${rows}.run-arrive`)).toHaveCount(0);

        await triggerRunViaAPI(page, TASK, daemonState.token);
        await expect(page.locator(rows).last()).toHaveClass(/run-arrive/);
    });

    test("a run finishing live slides into Recent activity", async ({
        authenticatedPage: page,
        daemonState,
    }) => {
        await page.goto("/");
        await expect(
            page.getByRole("heading", { name: "Recent activity", exact: true }),
        ).toBeVisible();
        // Page-load rows never animate.
        await expect(page.locator(".run-arrive")).toHaveCount(0);

        const run = await triggerRunViaAPI(page, TASK, daemonState.token);
        await waitForRunEnded(page, TASK, run.id, daemonState.token);
        await expect(page.locator("button.run-arrive").filter({ hasText: TASK })).toBeVisible();
    });
});
