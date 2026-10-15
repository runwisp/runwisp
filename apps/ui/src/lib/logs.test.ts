// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { LogEvent } from "@runwisp/ui";
import type { SSEStream } from "$lib/adapters/browser";
import { SSE_CONFIG } from "$lib/config/constants";
import { parseLogPage, streamRunLog, type LogPage } from "./logs";

// FakeStream records each opened log stream so a test can push SSE events.
class FakeStream implements SSEStream {
    readonly readyState = 1;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;
    closed = false;
    private readonly listeners = new Map<string, (event: MessageEvent) => void>();

    constructor(readonly url: string) {}

    close(): void {
        this.closed = true;
    }

    addEventListener(type: string, listener: (event: MessageEvent) => void): void {
        this.listeners.set(type, listener);
    }

    emit(type: string, data: unknown): void {
        this.listeners.get(type)?.(new MessageEvent(type, { data: JSON.stringify(data) }));
    }
}

const { streams } = vi.hoisted(() => ({ streams: new Array<FakeStream>() }));

vi.mock("$lib/adapters/browser", () => ({
    browserAuthEventSourceFactory: (url: string) => {
        const stream = new FakeStream(url);
        streams.push(stream);
        return stream;
    },
}));

// ─── parseLogPage ─────────────────────────────────────────────────────────────

describe("parseLogPage", () => {
    const makePage = (overrides: Partial<LogPage> = {}): LogPage => ({
        lines: [],
        firstAvailable: 0,
        totalLines: 0,
        truncated: false,
        finalized: false,
        ...overrides,
    });

    it("maps lines into a Record keyed by line number", () => {
        const page = makePage({
            lines: [
                { n: 0, ts: 0, stream: "stdout", text: "first" },
                { n: 1, ts: 0, stream: "stdout", text: "second" },
            ],
            totalLines: 2,
        });
        const evt = parseLogPage(page);
        expect(evt.lines).toEqual({ 0: "first", 1: "second" });
        expect(evt.sizeLines).toBe(2);
    });

    it("sets finished from page.finalized", () => {
        const page = makePage({ finalized: true });
        const evt = parseLogPage(page);
        expect(evt.finished).toBe(true);
    });

    it("does NOT set firstAvailableLine when firstAvailable is 0 (zero branch)", () => {
        const page = makePage({ firstAvailable: 0 });
        const evt = parseLogPage(page);
        expect(evt.firstAvailableLine).toBeUndefined();
    });

    it("sets firstAvailableLine when firstAvailable > 0 (non-zero branch)", () => {
        const page = makePage({ firstAvailable: 5 });
        const evt = parseLogPage(page);
        expect(evt.firstAvailableLine).toBe(5);
    });

    it("preserves stream and line-number gaps from sparse input", () => {
        const page = makePage({
            lines: [
                { n: 3, ts: 0, stream: "stderr", text: "boom" },
                { n: 7, ts: 0, stream: "stdout", text: "ok" },
            ],
            totalLines: 8,
        });
        const evt = parseLogPage(page);
        expect(evt.lines).toEqual({ 3: "boom", 7: "ok" });
        expect(Object.keys(evt.lines)).toHaveLength(2);
        expect(evt.sizeLines).toBe(8);
    });
});

// ─── streamRunLog ─────────────────────────────────────────────────────────────

describe("streamRunLog", () => {
    let events: LogEvent[];
    let close: () => void;

    function latest(): FakeStream {
        const stream = streams.at(-1);
        if (!stream) throw new Error("no stream opened");
        return stream;
    }

    beforeEach(() => {
        vi.useFakeTimers();
        streams.length = 0;
        events = [];
        close = streamRunLog("r1", (e) => events.push(e));
    });

    afterEach(() => {
        close();
        vi.useRealTimers();
    });

    it("opens on the tail and reports each line with the size it implies", () => {
        expect(latest().url).toBe("/api/runs/r1/log/stream?from=-1000");
        latest().emit("line", { n: 4, stream: "stdout", text: "hello", frameCount: 2 });
        latest().emit("line", "not a line");
        expect(events).toEqual([
            { lines: { 4: "hello" }, sizeLines: 5, finished: false, frameCounts: { 4: 2 } },
        ]);
    });

    it("resumes after the last line it saw when the stream drops", () => {
        latest().emit("line", { n: 9, stream: "stdout", text: "x" });
        latest().emit("region", { stream: "stdout", epoch: 1, rows: ["50%"] });
        latest().onerror?.(new Event("error"));
        vi.advanceTimersByTime(SSE_CONFIG.RECONNECT_DELAY);
        expect(streams).toHaveLength(2);
        expect(latest().url).toBe("/api/runs/r1/log/stream?from=10");
    });

    it("forwards region frames without a line", () => {
        latest().emit("region", { stream: "stdout", epoch: 3, rows: ["[###.] 75%"] });
        expect(events).toEqual([
            {
                lines: {},
                sizeLines: 0,
                finished: false,
                region: { stream: "stdout", epoch: 3, rows: ["[###.] 75%"] },
            },
        ]);
    });

    it("only moves the first available line forward on rotation", () => {
        latest().emit("rotated", { firstAvailable: 100 });
        latest().emit("rotated", { firstAvailable: 50 });
        latest().emit("line", { n: 120, stream: "stdout", text: "y" });
        expect(events.map((e) => e.firstAvailableLine)).toEqual([100, 100]);
    });

    it("finishes on done, sized to the final line, and stops reconnecting", () => {
        latest().emit("line", { n: 2, stream: "stdout", text: "z" });
        latest().emit("done", { finalLine: 7, status: "ended" });
        expect(events.at(-1)).toEqual({ lines: {}, sizeLines: 8, finished: true });
        expect(latest().closed).toBe(true);
        vi.advanceTimersByTime(SSE_CONFIG.MAX_RECONNECT_DELAY);
        expect(streams).toHaveLength(1);
    });

    it("closes the stream when the caller is done with it", () => {
        close();
        expect(latest().closed).toBe(true);
    });
});
