// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { LogCache } from "./LogCache.svelte.js";
import { WrapLayout } from "./wrap-layout.svelte.js";

// 10 columns: (width 96 - gutter 0 - padding 16) / charWidth 8.
const WIDTH = 96;
const CHAR_WIDTH = 8;

// Effects do not run under the node test environment, so lines are measured the
// way the console does for streamed text: through measureRange.
function setup(lines: string[], wrap: boolean) {
    const cache = new LogCache();
    cache.applyEvent({
        lines: Object.fromEntries(lines.map((text, i) => [i, text])),
        sizeLines: lines.length,
        finished: true,
    });
    const layout = new WrapLayout(cache, {
        wrap: () => wrap,
        width: () => WIDTH,
        gutterWidth: () => 0,
        charWidth: () => CHAR_WIDTH,
    });
    layout.measureRange(0, lines.length - 1);
    return { cache, layout };
}

describe("WrapLayout", () => {
    it("treats every line as one row when wrapping is off", () => {
        const { layout } = setup(["a".repeat(100), "b"], false);
        expect(layout.rowCount(0)).toBe(1);
        expect(layout.rowsAbove(1)).toBe(1);
        expect(layout.totalRows).toBe(2);
        expect(layout.offsetAt(1.5)).toBe(1);
        expect(layout.offsetEnd(0, 1.2)).toBe(2);
    });

    it("counts wrapped rows per line and accumulates positions", () => {
        // 25 columns wrap into 3 rows of 10; 5 columns stay on 1.
        const { layout } = setup(["a".repeat(25), "b".repeat(5), "c".repeat(10)], true);
        expect(layout.rowCount(0)).toBe(3);
        expect(layout.rowCount(1)).toBe(1);
        expect(layout.rowCount(2)).toBe(1);
        expect(layout.rowsAbove(0)).toBe(0);
        expect(layout.rowsAbove(1)).toBe(3);
        expect(layout.rowsAbove(2)).toBe(4);
        expect(layout.totalRows).toBe(5);
    });

    it("maps a scroll offset back to the line covering it", () => {
        const { layout } = setup(["a".repeat(25), "b".repeat(5), "c".repeat(10)], true);
        expect(layout.offsetAt(0)).toBe(0);
        expect(layout.offsetAt(2.9)).toBe(0);
        expect(layout.offsetAt(3)).toBe(1);
        expect(layout.offsetAt(4.5)).toBe(2);
        expect(layout.offsetEnd(0, 3.5)).toBe(2);
    });

    it("measures a range of lines that arrive later", () => {
        const { cache, layout } = setup([], true);
        const merged = cache.applyEvent({
            lines: { 0: "a".repeat(25) },
            sizeLines: 1,
            finished: false,
        });
        layout.measureRange(merged.min, merged.max);
        expect(layout.rowCount(0)).toBe(3);
        expect(layout.totalRows).toBe(3);
    });
});
