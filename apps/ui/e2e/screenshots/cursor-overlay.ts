// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
    // A modal <dialog> (showModal) renders in the browser's top layer, above any
    // z-index. So the cursor and pulse ring are manual popovers: also top layer,
    // painted above whatever was shown before them. These cancel the UA
    // [popover] box styles.
    const POPOVER_RESET =
        "right:auto;bottom:auto;border:0;padding:0;background:transparent;overflow:visible";
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
            "height:40px",
            "margin:0",
            "will-change:transform",
            // A short transition smooths sub-frame jitter between the many small
            // mouse.move() samples without adding a floaty lag.
            "transition:transform 28ms linear",
            "filter:drop-shadow(0 1px 1.5px rgba(0,0,0,0.3))",
            POPOVER_RESET,
        ].join(";");
        cursor.popover = "manual";
        // The macOS arrow, at its native 28x40 size. The clip paths in the
        // original were full-viewBox no-ops and are dropped.
        cursor.innerHTML =
            "<svg width='28' height='40' viewBox='0 0 28 40' xmlns='http://www.w3.org/2000/svg'>" +
            "<g fill='#fff'>" +
            "<path transform='matrix(1,0,0,-1,11.0557,14.0002)' d='M0 0H-1.085L.714-4.275C.941-4.814 .687-5.435 .148-5.662L.146-5.663C-.393-5.889-1.013-5.635-1.239-5.097L-3.054-.779-3.845-1.479-2.161-5.484C-1.84-6.25-1.094-6.745-.263-6.745 .011-6.745 .279-6.691 .535-6.584 1.042-6.371 1.436-5.973 1.644-5.463 1.852-4.954 1.849-4.394 1.636-3.888Z'/>" +
            "<path transform='matrix(1,0,0,-1,13.8846,12.142599)' d='M0 0-8.383 8.401C-8.937 8.956-9.885 8.564-9.885 7.78V-3.787C-9.885-5.231-8.181-5.998-7.1-5.042L-5.477-3.605-5.886-2.632-7.746-4.278C-8.188-4.669-8.885-4.355-8.885-3.765V7.132C-8.885 7.263-8.726 7.329-8.633 7.236L-.725-.689C-.295-1.121-.6-1.858-1.21-1.858H-3.914L-3.494-2.857-1.186-2.858C.306-2.858 1.053-1.056 0 0'/>" +
            "</g><g fill='#000'>" +
            "<path transform='matrix(1,0,0,-1,11.2036,19.662)' d='M0 0-.002-.001C-.541-.227-1.161 .026-1.387 .565L-3.852 6.429C-4.079 6.968-3.826 7.588-3.287 7.815-2.748 8.042-2.128 7.789-1.901 7.25L.566 1.387C.793 .847 .539 .226 0 0'/>" +
            "<path transform='matrix(1,0,0,-1,5,5.010601)' d='M0 0V-10.897C0-11.487 .697-11.801 1.139-11.41L3.874-8.989 7.674-8.99C8.284-8.99 8.59-8.253 8.159-7.821L.251 .104C.159 .197 0 .131 0 0'/>" +
            "</g></svg>";
        root.appendChild(cursor);
        cursor.showPopover();

        // Top-layer order is last-shown-wins, so anything shown later (a dialog,
        // a pulse ring) covers the cursor. Re-showing it moves it back on top.
        const raise = (): void => {
            cursor.hidePopover();
            cursor.showPopover();
        };
        new MutationObserver(() => {
            if (document.querySelector("dialog[open]")) raise();
        }).observe(root, { subtree: true, attributes: true, attributeFilter: ["open"] });

        const move = (x: number, y: number): void => {
            // Anchor the arrow tip (5px,5px into the svg) to the pointer.
            cursor.style.transform = `translate(${x - 5}px, ${y - 5}px)`;
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
                POPOVER_RESET,
                "border-radius:9999px",
                "border:2px solid rgba(21,160,168,0.9)",
                "background:rgba(21,160,168,0.25)",
            ].join(";");
            ring.popover = "manual";
            root.appendChild(ring);
            ring.showPopover();
            raise();
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
 * follows a smooth Bézier arc with a skewed bell-shaped speed curve and a slow
 * wobble, long reaches end with a small corrective hop, and clicks land off
 * center after a varying pause: the tells that separate a hand from a robot. Motion is driven by a seeded PRNG so every recording is
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

    /**
     * Glide to a locator (scrolling it into view first). Lands near the middle
     * but never dead-center, like a hand does; returns the landing point
     * relative to the element's box.
     */
    async moveOver(locator: Locator): Promise<{ x: number; y: number }> {
        await locator.scrollIntoViewIfNeeded().catch(() => {});
        const box = await locator.boundingBox();
        if (!box) throw new Error("moveOver: target has no bounding box");
        const x = box.width / 2 + (this.rand() - 0.5) * Math.min(box.width * 0.4, 48);
        const y = box.height / 2 + (this.rand() - 0.5) * Math.min(box.height * 0.4, 10);
        await this.moveTo(box.x + x, box.y + y);
        return { x, y };
    }

    /** Glide to a locator, pause a beat, then click it — the pulse fires on down. */
    async click(locator: Locator): Promise<void> {
        const position = await this.moveOver(locator);
        await this.page.waitForTimeout(110 + this.rand() * 150);
        // Click where the pointer already is; a bare click() would snap it to
        // the element's center first.
        await locator.click({ position });
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

    // Minimum-jerk position profile (the smoothest reach, a bell-shaped speed
    // curve), on time warped by t^0.8 so the speed peaks ~40% in: a hand
    // accelerates fast and spends longer decelerating onto the target.
    private static ease(t: number): number {
        const w = t ** 0.8;
        return w * w * w * (10 - 15 * w + 6 * w * w);
    }

    private async humanMove(tx: number, ty: number): Promise<void> {
        const dist = Math.hypot(tx - this.x, ty - this.y);
        // A long reach lands a little off target (usually short), then a quick
        // corrective sub-movement homes in, the way aimed movements really end.
        if (dist > 160) {
            const ux = (tx - this.x) / dist;
            const uy = (ty - this.y) / dist;
            const miss = dist * (0.03 + this.rand() * 0.04) * (this.rand() < 0.7 ? -1 : 1);
            const side = (this.rand() - 0.5) * Math.abs(miss);
            await this.stroke(tx + ux * miss - uy * side, ty + uy * miss + ux * side);
            await this.page.waitForTimeout(25 + this.rand() * 45);
        }
        await this.stroke(tx, ty);
    }

    private async stroke(tx: number, ty: number): Promise<void> {
        const sx = this.x;
        const sy = this.y;
        const dist = Math.hypot(tx - sx, ty - sy);
        if (dist < 1.5) {
            await this.page.mouse.move(tx, ty);
            this.x = tx;
            this.y = ty;
            return;
        }

        // Two control points bowed to the same side curve the path into a smooth
        // arc (the wrist pivots), more at the start than the end. Side and amount
        // vary per stroke.
        const nx = -(ty - sy) / dist;
        const ny = (tx - sx) / dist;
        const bow = (this.rand() - 0.5) * dist * 0.25;
        const c1x = sx + (tx - sx) * 0.3 + nx * bow;
        const c1y = sy + (ty - sy) * 0.3 + ny * bow;
        const c2x = sx + (tx - sx) * 0.7 + nx * bow * 0.6;
        const c2y = sy + (ty - sy) * 0.7 + ny * bow * 0.6;

        // Fitts's law: time grows with log distance, so short hops are quick and
        // long reaches don't drag.
        const duration = 40 + 70 * Math.log2(1 + dist / 16);
        const steps = Math.max(6, Math.round(duration / 16));
        const delay = duration / steps;

        // A slow, smooth sideways wobble (two sines, random phase), zero at both
        // ends so take-off and landing stay exact. Smooth, unlike per-sample
        // noise, which reads as jitter.
        const amp = Math.min(2.5, dist * 0.012);
        const p1 = this.rand() * Math.PI * 2;
        const p2 = this.rand() * Math.PI * 2;

        for (let i = 1; i < steps; i++) {
            const t = DemoCursor.ease(i / steps);
            const u = 1 - t;
            const wobble =
                amp *
                Math.sin((Math.PI * i) / steps) *
                (0.7 * Math.sin(p1 + i * 0.55) + 0.3 * Math.sin(p2 + i * 1.3));
            const px = u * u * u * sx + 3 * u * u * t * c1x + 3 * u * t * t * c2x + t * t * t * tx;
            const py = u * u * u * sy + 3 * u * u * t * c1y + 3 * u * t * t * c2y + t * t * t * ty;
            await this.page.mouse.move(px + nx * wobble, py + ny * wobble);
            await this.page.waitForTimeout(delay);
        }
        await this.page.mouse.move(tx, ty);
        await this.page.waitForTimeout(delay);
        this.x = tx;
        this.y = ty;
    }
}
