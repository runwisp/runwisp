// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi, afterEach } from "vitest";
import { createLogger } from "@runwisp/common";
import type { SSEStream } from "$lib/adapters/browser";
import type { SSEErrorInfo } from "./event-source";
import { createReconnectingConnection, type ReconnectingConnection } from "./sse-reconnect";

// ─── FakeEventSource ──────────────────────────────────────────────────────────

class FakeEventSource implements SSEStream {
    readonly readyState = 1;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;
    closed = false;

    close(): void {
        this.closed = true;
    }

    addEventListener(): void {}

    fireOpen(): void {
        this.onopen?.(new Event("open"));
    }

    fireError(evt: Event = new Event("error")): void {
        this.onerror?.(evt);
    }
}

const openConnections: ReconnectingConnection[] = [];

afterEach(() => {
    for (const conn of openConnections.splice(0)) conn.dispose();
    vi.restoreAllMocks();
    vi.useRealTimers();
});

function connect(opts: {
    path?: () => string;
    createEventSource: (url: string) => SSEStream;
    onCreated?: (es: SSEStream) => void;
    onOpen?: () => void;
    onError?: (info: SSEErrorInfo) => void;
}): ReconnectingConnection {
    const conn = createReconnectingConnection({
        resolve: () => {
            const url = opts.path?.() ?? "/test";
            return { url, label: url };
        },
        createEventSource: opts.createEventSource,
        logger: createLogger("test"),
        onCreated: opts.onCreated ?? (() => undefined),
        onOpen: opts.onOpen ?? (() => undefined),
        onError: opts.onError ?? (() => undefined),
        shouldReconnect: () => true,
    });
    conn.connect();
    openConnections.push(conn);
    return conn;
}

// ─── createReconnectingConnection ─────────────────────────────────────────────

describe("createReconnectingConnection", () => {
    it("resolves the path and connects to it", () => {
        const pathFn = vi.fn().mockReturnValue("/dynamic-path");
        const factory = vi.fn().mockReturnValue(new FakeEventSource());
        connect({ path: pathFn, createEventSource: factory });
        expect(pathFn).toHaveBeenCalled();
        expect(factory).toHaveBeenCalledWith("/dynamic-path");
    });

    it("hands the new stream to onCreated", () => {
        const es = new FakeEventSource();
        const created: SSEStream[] = [];
        connect({ createEventSource: () => es, onCreated: (s) => created.push(s) });
        expect(created).toEqual([es]);
    });

    it("calls onOpen when the stream opens", () => {
        const es = new FakeEventSource();
        const onOpen = vi.fn();
        connect({ createEventSource: () => es, onOpen });
        es.fireOpen();
        expect(onOpen).toHaveBeenCalledOnce();
    });

    it("dispose closes the stream", () => {
        const es = new FakeEventSource();
        const conn = connect({ createEventSource: () => es });
        conn.dispose();
        expect(es.closed).toBe(true);
    });

    it("dispose during the reconnect backoff cancels the reconnect", () => {
        vi.useFakeTimers();
        let callCount = 0;
        const conn = connect({
            createEventSource: () => {
                callCount++;
                return new FakeEventSource();
            },
        });
        const first = conn.getStream();
        if (!(first instanceof FakeEventSource)) throw new Error("no stream");
        first.fireError();
        conn.dispose();
        vi.runAllTimers();
        expect(callCount).toBe(1);
    });

    it("calls onError with extracted info when the connection errors", () => {
        const es = new FakeEventSource();
        const errors: SSEErrorInfo[] = [];
        connect({ createEventSource: () => es, onError: (info) => errors.push(info) });
        es.fireError(Object.assign(new Event("error"), { status: 503 }));
        expect(errors).toHaveLength(1);
        expect(errors[0]?.status).toBe(503);
    });

    it("schedules a reconnect on error", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        connect({
            createEventSource: () => {
                const es = new FakeEventSource();
                esList.push(es);
                return es;
            },
        });
        esList[0]?.fireError();
        expect(esList).toHaveLength(1);
        vi.advanceTimersByTime(3001);
        expect(esList).toHaveLength(2);
    });

    it("keeps reconnecting under the backoff ceiling", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        connect({
            createEventSource: () => {
                const es = new FakeEventSource();
                esList.push(es);
                return es;
            },
        });
        for (let i = 0; i < 10; i++) {
            esList[esList.length - 1]?.fireError();
            vi.advanceTimersByTime(60000);
        }
        expect(esList.length).toBeGreaterThan(1);
    });

    it("handles createEventSource throwing by calling onError and scheduling a reconnect", () => {
        vi.useFakeTimers();
        const errors: SSEErrorInfo[] = [];
        let callCount = 0;
        connect({
            onError: (info) => errors.push(info),
            createEventSource: () => {
                callCount++;
                if (callCount === 1) throw new Error("network unavailable");
                return new FakeEventSource();
            },
        });
        expect(errors).toHaveLength(1);
        expect(errors[0]?.message).toContain("network unavailable");
        vi.advanceTimersByTime(3001);
        expect(callCount).toBe(2);
    });
});
