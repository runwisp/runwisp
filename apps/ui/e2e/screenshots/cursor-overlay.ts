// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A synthetic on-screen cursor for the demo-video recording. Playwright drives a
// real mouse (dispatching mousemove/mousedown), but the browser renders no
// pointer into the captured video — so this injects a fixed-position arrow that
// tracks those events, plus a click "pulse" ring, so viewers can follow what's
// being clicked. Purely cosmetic; only used by *.demo-video.ts.

import { type Page, type Locator } from "@playwright/test";

// The canonical resting spot on the overview. The tour opens and closes with the
// cursor parked exactly here so the WebP loops almost seamlessly (same view,
// same pointer position at both ends).
export const CURSOR_HOME = { x: 210, y: 150 } as const;

// The init script runs in the browser at document-start on every navigation, so
// the cursor survives page.goto()s. It attaches to <html> (not <body>) to
// outlive SvelteKit re-renders of the app root.
function overlayInitScript(): void {
    const ID = "__rw_demo_cursor__";
    function ensure(): void {
        const root = document.documentElement;
        if (!root || document.getElementById(ID)) return;

        const cursor = document.createElement("div");
        cursor.id = ID;
        cursor.style.cssText = [
            "position:fixed",
            "top:0",
            "left:0",
            "z-index:2147483647",
            "pointer-events:none",
            "width:28px",
            "height:28px",
            "margin:0",
            "will-change:transform",
            // A short transition smooths sub-frame jitter between the many small
            // mouse.move() samples without adding a floaty lag.
            "transition:transform 28ms linear",
            "filter:drop-shadow(0 2px 3px rgba(0,0,0,0.35))",
        ].join(";");
        cursor.innerHTML =
            "<svg width='28' height='28' viewBox='0 0 24 24' fill='none' xmlns='http://www.w3.org/2000/svg'>" +
            "<path d='M5 3.5 L5 19.5 L9.1 15.6 L11.8 21.2 L14.4 20 L11.7 14.4 L17.4 14.4 Z' " +
            "fill='white' stroke='#111827' stroke-width='1.3' stroke-linejoin='round'/></svg>";
        root.appendChild(cursor);

        const move = (x: number, y: number): void => {
            // Anchor the arrow tip (~2px,2px into the svg) to the pointer.
            cursor.style.transform = `translate(${x - 2}px, ${y - 2}px)`;
        };

        const pulse = (x: number, y: number): void => {
            const ring = document.createElement("div");
            ring.style.cssText = [
                "position:fixed",
                `left:${x}px`,
                `top:${y}px`,
                "z-index:2147483646",
                "pointer-events:none",
                "width:14px",
                "height:14px",
                "margin:-7px 0 0 -7px",
                "border-radius:9999px",
                "border:2px solid rgba(21,160,168,0.9)",
                "background:rgba(21,160,168,0.25)",
            ].join(";");
            root.appendChild(ring);
            ring.animate(
                [
                    { transform: "scale(0.3)", opacity: 0.9 },
                    { transform: "scale(2.6)", opacity: 0 },
                ],
                { duration: 480, easing: "cubic-bezier(0.22, 0.61, 0.36, 1)" },
            ).onfinish = () => ring.remove();
        };

        window.addEventListener("mousemove", (e) => move(e.clientX, e.clientY), true);
        window.addEventListener("mousedown", (e) => pulse(e.clientX, e.clientY), true);
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", ensure);
    } else {
        ensure();
    }
    window.addEventListener("load", ensure);
}

/**
 * A stateful pointer that drives Playwright's mouse in curved, human-looking
 * strokes and keeps the injected overlay in sync across navigations. One per
 * page.
 *
 * Real pointer motion isn't a straight line at constant speed. Each stroke here
 * follows a gentle Bézier arc, accelerates then decelerates (a bell-shaped
 * velocity profile), carries a small tremor that fades as it homes in, and
 * lightly overshoots a distant target before settling — the tells that separate
 * a hand from a robot. Motion is driven by a seeded PRNG so every recording is
 * bit-for-bit reproducible.
 */
export class DemoCursor {
    private x = CURSOR_HOME.x;
    private y = CURSOR_HOME.y;
    // xorshift32 state — deterministic pseudo-randomness (no Math.random), so the
    // arcs and tremor look organic yet reproduce identically across recordings.
    private seed = 0x1a2b3c4d;

    private constructor(private readonly page: Page) {}

    /** Inject the overlay and park the pointer at the home position. */
    static async install(page: Page): Promise<DemoCursor> {
        await page.addInitScript(overlayInitScript);
        return new DemoCursor(page);
    }

    /** Re-render the overlay after a navigation replaced the DOM. */
    async settle(): Promise<void> {
        await this.page.mouse.move(this.x, this.y);
    }

    /** Glide smoothly to an absolute viewport point along a human-like arc. */
    async moveTo(x: number, y: number): Promise<void> {
        await this.humanMove(x, y);
    }

    /** Glide to the center of a locator (scrolling it into view first). */
    async moveOver(locator: Locator): Promise<void> {
        await locator.scrollIntoViewIfNeeded().catch(() => {});
        const box = await locator.boundingBox();
        if (!box) throw new Error("moveOver: target has no bounding box");
        await this.moveTo(box.x + box.width / 2, box.y + box.height / 2);
    }

    /** Glide to a locator, pause a beat, then click it — the pulse fires on down. */
    async click(locator: Locator): Promise<void> {
        await this.moveOver(locator);
        await this.page.waitForTimeout(160);
        await locator.click();
    }

    /** Return to the canonical overview resting spot (used to close the loop). */
    async home(): Promise<void> {
        await this.moveTo(CURSOR_HOME.x, CURSOR_HOME.y);
    }

    /** Deterministic float in [0, 1). */
    private rand(): number {
        let s = this.seed | 0;
        s ^= s << 13;
        s ^= s >>> 17;
        s ^= s << 5;
        this.seed = s | 0;
        return ((s >>> 0) % 100000) / 100000;
    }

    // Cosine ease-in-out: slow near both ends, fast through the middle. Sampling
    // the Bézier at eased t (with constant real-time delay per step) yields the
    // bell-shaped velocity of a real hand.
    private static ease(t: number): number {
        return (1 - Math.cos(Math.PI * t)) / 2;
    }

    private async humanMove(tx: number, ty: number): Promise<void> {
        const sx = this.x;
        const sy = this.y;
        const dist = Math.hypot(tx - sx, ty - sy);
        if (dist < 1.5) {
            await this.page.mouse.move(tx, ty);
            this.x = tx;
            this.y = ty;
            return;
        }

        // A hand overshoots a far target slightly and corrects; aim the arc a few
        // px past it along the travel direction, then settle back at the end.
        const overshoot = dist > 220 ? 6 + this.rand() * 8 : 0;
        const ux = (tx - sx) / dist;
        const uy = (ty - sy) / dist;
        const ax = tx + ux * overshoot;
        const ay = ty + uy * overshoot;

        // One control point offset perpendicular to travel bows the path into a
        // gentle arc instead of a ruler-straight line. Side and amount vary.
        const nx = -uy;
        const ny = ux;
        const bow = (this.rand() - 0.5) * dist * 0.16;
        const cx = (sx + ax) / 2 + nx * bow;
        const cy = (sy + ay) / 2 + ny * bow;

        // Fitts-ish timing: sub-linear in distance, clamped to a snappy range —
        // a real hand flicks across the screen fast, then eases onto the target.
        const duration = Math.min(440, Math.max(150, 45 + dist * 0.45));
        const steps = Math.max(8, Math.round(duration / 16));
        const delay = duration / steps;

        for (let i = 1; i <= steps; i++) {
            const t = DemoCursor.ease(i / steps);
            const u = 1 - t;
            let px = u * u * sx + 2 * u * t * cx + t * t * ax;
            let py = u * u * sy + 2 * u * t * cy + t * t * ay;
            // Tremor that fades to zero as the pointer homes in, so the landing
            // stays precise while the travel feels alive.
            const tremor = (1 - t) * 1.1;
            px += (this.rand() - 0.5) * tremor;
            py += (this.rand() - 0.5) * tremor;
            await this.page.mouse.move(px, py);
            await this.page.waitForTimeout(delay);
        }

        // Corrective settle onto the exact target after any overshoot.
        if (overshoot > 0) {
            await this.page.mouse.move((ax + tx) / 2, (ay + ty) / 2);
            await this.page.waitForTimeout(delay);
        }
        await this.page.mouse.move(tx, ty);
        this.x = tx;
        this.y = ty;
    }
}
