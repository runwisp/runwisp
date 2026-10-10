// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import {
    FOLD_DISTANCE,
    FOLD_MS,
    HeaderFold,
    fitsUnfolded,
    foldStep,
    measuredStripDelta,
} from "./header-fold.svelte";

const mid = { atTop: false, atBottom: false };

describe("foldStep", () => {
    it("folds after FOLD_DISTANCE down without reversing", () => {
        expect(foldStep(0, { ...mid, delta: 100 })).toEqual({ run: 100 });
        expect(foldStep(100, { ...mid, delta: FOLD_DISTANCE - 100 })).toEqual({
            run: 0,
            fold: true,
        });
    });

    it("unfolds after FOLD_DISTANCE up", () => {
        expect(foldStep(-200, { ...mid, delta: -40 })).toEqual({ run: 0, fold: false });
    });

    it("starts over when the direction reverses", () => {
        expect(foldStep(200, { ...mid, delta: -10 })).toEqual({ run: -10 });
    });

    it("folds at the bottom and unfolds at the top right away", () => {
        expect(foldStep(0, { delta: 5, atTop: false, atBottom: true })).toEqual({
            run: 0,
            fold: true,
        });
        expect(foldStep(0, { delta: -5, atTop: true, atBottom: false })).toEqual({
            run: 0,
            fold: false,
        });
    });
});

describe("fitsUnfolded", () => {
    it("judges a folded log by the room it has once unfolded", () => {
        expect(fitsUnfolded(500, 600, false, 120)).toBe(true);
        expect(fitsUnfolded(500, 600, true, 120)).toBe(false);
        expect(fitsUnfolded(480, 600, true, 120)).toBe(true);
    });
});

describe("measuredStripDelta", () => {
    const desktop = { phone: false, foldedHeight: 0 };

    it("takes the full strip height on desktop while unfolded", () => {
        expect(measuredStripDelta(0, { ...desktop, folded: false, fullHeight: 96 })).toBe(96);
    });

    it("keeps the unfolded height while the strip slides out", () => {
        // The slide reports the strip shrinking toward 0 once folded.
        let delta = measuredStripDelta(0, { ...desktop, folded: false, fullHeight: 96 });
        for (const h of [70, 30, 0]) {
            delta = measuredStripDelta(delta, { ...desktop, folded: true, fullHeight: h });
        }
        expect(delta).toBe(96);
        // So a log that fits only folded still counts as not fitting unfolded.
        expect(fitsUnfolded(550, 600, true, delta)).toBe(false);
    });

    it("takes the difference to the one-line strip on a phone", () => {
        const m = { phone: true, folded: true, fullHeight: 140, foldedHeight: 40 };
        expect(measuredStripDelta(0, m)).toBe(100);
        expect(measuredStripDelta(0, { ...m, fullHeight: 20 })).toBe(0);
    });
});

describe("HeaderFold", () => {
    function setup() {
        let now = 1000;
        const fold = new HeaderFold(() => now);
        const el = { scrollTop: 500, scrollHeight: 5000, clientHeight: 400 };
        const scroll = (top: number) => {
            el.scrollTop = top;
            fold.scrolled(el);
        };
        return {
            fold,
            el,
            scroll,
            tick: (ms: number) => (now += ms),
            read: () => {
                fold.intent();
                scroll(el.scrollTop);
            },
        };
    }

    it("folds when the reader scrolls 240px down", () => {
        const { fold, scroll, read } = setup();
        read();
        scroll(600);
        expect(fold.folded).toBe(false);
        scroll(500 + FOLD_DISTANCE);
        expect(fold.folded).toBe(true);
    });

    it("ignores scrolls the reader didn't make", () => {
        const { fold, scroll } = setup();
        scroll(500);
        scroll(4600);
        expect(fold.folded).toBe(false);
    });

    it("ignores a scroll that comes with a size change (a live log growing)", () => {
        const { fold, el, scroll, read } = setup();
        read();
        el.scrollHeight = 6000;
        scroll(5600);
        expect(fold.folded).toBe(false);
    });

    it("keeps counting momentum after the last input", () => {
        const { fold, scroll, read, tick } = setup();
        read();
        for (let top = 600; top <= 800; top += 100) {
            tick(200);
            scroll(top);
        }
        expect(fold.folded).toBe(true);
    });

    it("ignores scrolls while the fold settles, then counts again", () => {
        const { fold, scroll, read, tick } = setup();
        fold.set(true);
        read();
        scroll(0);
        expect(fold.folded).toBe(true);
        tick(FOLD_MS + 50);
        read();
        scroll(300);
        scroll(0);
        expect(fold.folded).toBe(false);
    });

    it("never folds over a log that fits, whatever else scrolls", () => {
        const { fold, scroll, read } = setup();
        fold.findLog = () => ({ dataset: { contentHeight: "250" }, clientHeight: 400 });
        read();
        scroll(500 + FOLD_DISTANCE);
        scroll(4600);
        expect(fold.folded).toBe(false);

        // A log that needs scrolling lets the same scroll fold.
        fold.findLog = () => ({ dataset: { contentHeight: "900" }, clientHeight: 400 });
        read();
        scroll(4000);
        scroll(4600);
        expect(fold.folded).toBe(true);
    });

    it("unfolds a short log once the fold has settled", () => {
        const { fold, tick } = setup();
        fold.stripDelta = 100;
        fold.set(true);
        const log = { dataset: { contentHeight: "250" }, clientHeight: 400 };
        fold.checkShortLog(log);
        expect(fold.folded).toBe(true);
        tick(FOLD_MS + 50);
        fold.checkShortLog(log);
        expect(fold.folded).toBe(false);
    });
});
