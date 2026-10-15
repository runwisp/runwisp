// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { LogCache } from "./LogCache.svelte";

describe("LogCache", () => {
    it("merges a slice, skipping bad keys and reporting what changed", () => {
        const cache = new LogCache();
        expect(cache.mergeSlice({ 2: "two", 5: "five", [-1]: "neg" })).toEqual({
            touched: true,
            min: 2,
            max: 5,
        });
        expect(cache.size).toBe(2);
        expect(cache.mergeSlice({ 2: "two" }).touched).toBe(false);
    });

    it("keeps the widest line width even after that line is gone", () => {
        const cache = new LogCache();
        cache.mergeSlice({ 0: "x".repeat(40) });
        cache.mergeSlice({ 0: "short" });
        expect(cache.maxLineColumns).toBe(40);
    });

    it("only grows totals and the first available line from events", () => {
        const cache = new LogCache();
        cache.applyEvent({
            lines: {},
            sizeLines: 10,
            sizeBytes: 300,
            finished: false,
            firstAvailableLine: 4,
        });
        cache.applyEvent({
            lines: {},
            sizeLines: 8,
            sizeBytes: 100,
            finished: false,
            firstAvailableLine: 2,
        });
        expect([cache.totalLines, cache.totalBytes, cache.firstAvailableLine]).toEqual([
            10, 300, 4,
        ]);
    });

    it("records frame history only for lines that have some", () => {
        const cache = new LogCache();
        cache.applyEvent({
            lines: { 3: "100%" },
            sizeLines: 4,
            finished: false,
            frameCounts: { 3: 12, 1: 0 },
        });
        expect([...cache.frameCounts]).toEqual([[3, 12]]);
    });

    it("orders the live overlay stdout, stderr, then other streams by name", () => {
        const cache = new LogCache();
        cache.applyRegion({ stream: "zeta", epoch: 1, rows: ["z"] });
        cache.applyRegion({ stream: "stderr", epoch: 1, rows: ["e"] });
        cache.applyRegion({ stream: "alpha", epoch: 1, rows: ["a"] });
        cache.applyRegion({ stream: "stdout", epoch: 1, rows: ["o1", "o2"] });
        expect(cache.overlayRows).toEqual(["o1", "o2", "e", "a", "z"]);

        cache.applyRegion({ stream: "zeta", epoch: 2, rows: [] });
        expect(cache.overlayRows).toEqual(["o1", "o2", "e", "a"]);
    });

    it("keeps region frames out of the committed lines and drops them once finished", () => {
        const cache = new LogCache();
        const res = cache.applyEvent({
            lines: {},
            sizeLines: 0,
            finished: false,
            region: { stream: "stdout", epoch: 1, rows: ["[##..] 50%"] },
        });
        expect(res.touched).toBe(false);
        expect(cache.size).toBe(0);
        expect(cache.overlayRows).toEqual(["[##..] 50%"]);

        cache.applyEvent({ lines: { 0: "done" }, sizeLines: 1, finished: true });
        expect(cache.overlayRows).toEqual([]);
        expect(cache.finished).toBe(true);
    });

    it("prunes lines far from the viewport once the cache is full", () => {
        const cache = new LogCache();
        const slice: Record<number, string> = {};
        for (let n = 0; n < cache.MAX_CACHE_SIZE; n++) slice[n] = "l";
        cache.mergeSlice(slice);

        cache.prune(40_000, 40_100);
        expect(cache.lines.has(40_050)).toBe(true);
        expect(cache.lines.has(0)).toBe(false);
        expect(cache.size).toBeLessThan(cache.MAX_CACHE_SIZE);
    });

    it("leaves a cache under the limit alone", () => {
        const cache = new LogCache();
        cache.mergeSlice({ 0: "a", 99_999: "b" });
        cache.prune(50_000, 50_010);
        expect(cache.size).toBe(2);
    });

    it("judges a range complete within what the server still holds", () => {
        const cache = new LogCache();
        cache.applyEvent({
            lines: { 5: "a", 6: "b" },
            sizeLines: 7,
            finished: false,
            firstAvailableLine: 5,
        });
        expect(cache.isRangeComplete(0, 100)).toBe(true);
        cache.applyEvent({ lines: {}, sizeLines: 8, finished: false });
        expect(cache.isRangeComplete(0, 100)).toBe(false);
    });

    it("resets to empty", () => {
        const cache = new LogCache();
        cache.applyEvent({
            lines: { 0: "a" },
            sizeLines: 1,
            finished: true,
            firstAvailableLine: 1,
        });
        cache.reset();
        expect([cache.size, cache.totalLines, cache.finished, cache.firstAvailableLine]).toEqual([
            0,
            0,
            false,
            0,
        ]);
    });
});
