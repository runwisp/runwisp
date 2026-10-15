// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { LogCache } from "./LogCache.svelte";
import { LogFetcher } from "./LogFetcher.svelte";
import type { LogEvent, LogSlice } from "./types";

const CHUNK = 4096;

/** A cache that knows the log is `total` lines long but holds none of them. */
function emptyCache(total: number): LogCache {
    const cache = new LogCache();
    cache.applyEvent({ lines: {}, sizeLines: total, finished: false });
    return cache;
}

/** A fetch whose calls stay pending until the test settles them. */
function deferredFetch() {
    const pending: { from: number; to: number; settle: (v: LogSlice | LogEvent) => void }[] = [];
    const fn = vi.fn(
        (from: number, to: number) =>
            new Promise<LogSlice | LogEvent>((resolve) => {
                pending.push({ from, to, settle: resolve });
            }),
    );
    return { fn, pending };
}

const live: LogFetcher[] = [];

function start(...args: ConstructorParameters<typeof LogFetcher>): LogFetcher {
    const fetcher = new LogFetcher(...args);
    live.push(fetcher);
    return fetcher;
}

beforeEach(() => {
    vi.useFakeTimers();
});

afterEach(() => {
    for (const fetcher of live.splice(0)) fetcher.destroy();
    vi.useRealTimers();
});

describe("LogFetcher", () => {
    it("fetches a missing chunk once, merges it, and reports the loaded range", async () => {
        const cache = emptyCache(10);
        const onLoaded = vi.fn();
        const fetchLogs = vi.fn(() => ({ 0: "a", 9: "j" }));
        const fetcher = start(cache, fetchLogs, onLoaded);

        fetcher.enqueue(0, 9);
        fetcher.enqueue(2, 5); // same chunk, already queued
        await vi.runAllTimersAsync();

        expect(fetchLogs).toHaveBeenCalledExactlyOnceWith(0, 9);
        expect(cache.lines.get(9)).toBe("j");
        expect(onLoaded).toHaveBeenCalledWith(0, 9);
        expect(fetcher.isFetching).toBe(false);
    });

    it("applies an event response, not just a slice", async () => {
        const cache = emptyCache(1);
        const fetcher = start(cache, () => ({ lines: { 0: "x" }, sizeLines: 3, finished: true }));
        fetcher.enqueue(0, 0);
        await vi.runAllTimersAsync();
        expect([cache.totalLines, cache.finished]).toEqual([3, true]);
    });

    it("skips a range the cache already holds", async () => {
        const cache = emptyCache(2);
        cache.mergeSlice({ 0: "a", 1: "b" });
        const fetchLogs = vi.fn(() => ({}));
        const fetcher = start(cache, fetchLogs);
        fetcher.enqueue(0, 1);
        await vi.runAllTimersAsync();
        expect(fetchLogs).not.toHaveBeenCalled();
    });

    it("never asks for lines rotated away", async () => {
        const cache = emptyCache(CHUNK * 2);
        cache.applyEvent({
            lines: {},
            sizeLines: CHUNK * 2,
            finished: false,
            firstAvailableLine: CHUNK + 5,
        });
        const fetchLogs = vi.fn(() => ({}));
        const fetcher = start(cache, fetchLogs);
        fetcher.enqueue(0, CHUNK * 2 - 1);
        await vi.runAllTimersAsync();
        expect(fetchLogs.mock.calls).toEqual([[CHUNK, CHUNK * 2 - 1]]);
    });

    it("keeps at most two chunks in flight", async () => {
        const { fn, pending } = deferredFetch();
        const fetcher = start(emptyCache(CHUNK * 3), fn);

        fetcher.enqueue(0, 0);
        await vi.advanceTimersByTimeAsync(fetcher.MIN_REQUEST_INTERVAL_MS);
        fetcher.enqueue(CHUNK, CHUNK * 3 - 1);
        await vi.advanceTimersByTimeAsync(fetcher.MIN_REQUEST_INTERVAL_MS * 5);
        expect(fn).toHaveBeenCalledTimes(2);
        expect(fetcher.isFetching).toBe(true);

        pending[0]?.settle({});
        await vi.advanceTimersByTimeAsync(fetcher.MIN_REQUEST_INTERVAL_MS * 5);
        expect(fn).toHaveBeenCalledTimes(3);
    });

    it("drops queued chunks the viewport has left behind", async () => {
        const fetchLogs = vi.fn((_from: number, _to: number) => ({}));
        const fetcher = start(emptyCache(CHUNK * 10), fetchLogs);
        fetcher.enqueue(0, CHUNK * 10 - 1);
        fetcher.pruneQueue(CHUNK * 9, CHUNK * 10 - 1);
        await vi.runAllTimersAsync();
        expect(fetchLogs.mock.calls.map(([from]) => from / CHUNK)).toEqual([7, 8, 9]);
    });

    it("recovers from a failed fetch so the chunk can be asked for again", async () => {
        const fetchLogs = vi
            .fn()
            .mockRejectedValueOnce(new Error("503"))
            .mockResolvedValue({ 0: "a" });
        const cache = emptyCache(1);
        const fetcher = start(cache, fetchLogs);
        fetcher.enqueue(0, 0);
        await vi.runAllTimersAsync();
        expect(fetcher.isFetching).toBe(false);
        fetcher.enqueue(0, 0);
        await vi.runAllTimersAsync();
        expect(cache.lines.get(0)).toBe("a");
    });

    it("requests only the incomplete chunks in view, up to the cap", async () => {
        const cache = emptyCache(CHUNK * 4);
        const filled: Record<number, string> = {};
        for (let n = CHUNK; n < CHUNK * 2; n++) filled[n] = "x";
        cache.mergeSlice(filled);
        const fetchLogs = vi.fn((_from: number, _to: number) => ({}));
        const fetcher = start(cache, fetchLogs);

        fetcher.maybeRequestMissing(0, CHUNK * 4 - 1, 3);
        await vi.runAllTimersAsync();
        expect(fetchLogs.mock.calls.map(([from]) => from / CHUNK)).toEqual([0, 2]);
    });

    it("does nothing without a fetch function or after destroy", async () => {
        const fetchLogs = vi.fn(() => ({}));
        new LogFetcher(emptyCache(5), undefined).enqueue(0, 4);
        const fetcher = start(emptyCache(5), fetchLogs);
        fetcher.enqueue(0, 4);
        fetcher.destroy();
        await vi.runAllTimersAsync();
        expect(fetchLogs).not.toHaveBeenCalled();
    });
});
