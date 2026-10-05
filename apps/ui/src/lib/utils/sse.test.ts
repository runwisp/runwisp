// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi, afterEach } from "vitest";
import type { SSEStream } from "$lib/adapters/browser";
import type { SSEErrorInfo } from "./event-source";
import { connectSSE } from "./sse";

// ErrorEvent polyfill — not available in the Node test environment
if (typeof Reflect.get(globalThis, "ErrorEvent") === "undefined") {
    Reflect.set(
        globalThis,
        "ErrorEvent",
        class ErrorEventPolyfill extends Event {
            readonly message: string;
            constructor(type: string, init?: { message?: string }) {
                super(type);
                this.message = init?.message ?? "";
            }
        },
    );
}

// ─── FakeEventSource ──────────────────────────────────────────────────────────

class FakeEventSource implements SSEStream {
    readonly readyState = 1;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;

    readonly #target = new EventTarget();
    closed = false;

    close(): void {
        this.closed = true;
    }

    addEventListener(
        type: string,
        listener: (event: MessageEvent) => void,
        options?: boolean | AddEventListenerOptions,
    ): void {
        this.#target.addEventListener(
            type,
            (e: Event) => {
                if (e instanceof MessageEvent) listener(e);
            },
            options,
        );
    }

    fireOpen(): void {
        this.onopen?.(new Event("open"));
    }

    fireError(evt: Event = new Event("error")): void {
        this.onerror?.(evt);
    }

    fireNamedEvent(type: string, data: string): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data }));
    }

    fireNamedEventRaw(type: string, data: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data }));
    }
}

const openConnections: { disconnect(): void }[] = [];

afterEach(() => {
    for (const conn of openConnections.splice(0)) conn.disconnect();
    vi.restoreAllMocks();
    vi.useRealTimers();
});

function connect(options: Parameters<typeof connectSSE>[0]) {
    const conn = connectSSE(options);
    openConnections.push(conn);
    return conn;
}

// ─── connectSSE ───────────────────────────────────────────────────────────────

describe("connectSSE", () => {
    function makeConnection(
        opts: {
            eventTypes?: string[];
            onOpen?: () => void;
            onError?: (info: SSEErrorInfo) => void;
        } = {},
    ) {
        const es = new FakeEventSource();
        const events: [string, string][] = [];

        const conn = connect({
            path: () => "/test",
            eventTypes: opts.eventTypes ?? [],
            onEvent: (t, d) => events.push([t, d]),
            ...(opts.onOpen !== undefined ? { onOpen: opts.onOpen } : {}),
            ...(opts.onError !== undefined ? { onError: opts.onError } : {}),
            createEventSource: () => es,
        });

        return { conn, es, events };
    }

    it("calls path() and connects to the resolved path", () => {
        const pathFn = vi.fn().mockReturnValue("/dynamic-path");
        const factory = vi.fn().mockReturnValue(new FakeEventSource());
        connect({
            path: pathFn,
            eventTypes: [],
            onEvent: () => {},
            createEventSource: factory,
        });
        expect(pathFn).toHaveBeenCalled();
        expect(factory).toHaveBeenCalledWith("/dynamic-path");
    });

    it("calls onOpen when the stream opens", () => {
        let openCalled = false;
        const { es } = makeConnection({
            onOpen: () => {
                openCalled = true;
            },
        });
        es.fireOpen();
        expect(openCalled).toBe(true);
    });

    it("dispatches named events via addEventListener", () => {
        const { es, events } = makeConnection({ eventTypes: ["line", "done"] });
        es.fireNamedEvent("line", '{"text":"hello"}');
        es.fireNamedEvent("done", '{"status":"ok"}');
        expect(events).toEqual([
            ["line", '{"text":"hello"}'],
            ["done", '{"status":"ok"}'],
        ]);
    });

    it("ignores named event with non-string data (data===undefined branch)", () => {
        const { es, events } = makeConnection({ eventTypes: ["line"] });
        es.fireNamedEventRaw("line", 42); // non-string data → getMessageEventData returns undefined
        expect(events).toHaveLength(0);
    });

    it("disconnect closes the EventSource", () => {
        const { conn, es } = makeConnection();
        conn.disconnect();
        expect(es.closed).toBe(true);
    });

    it("disconnect during reconnect timeout cancels the timeout", () => {
        vi.useFakeTimers();
        const es1 = new FakeEventSource();
        const es2 = new FakeEventSource();
        let callCount = 0;
        const conn = connect({
            path: () => "/test",
            eventTypes: [],
            onEvent: () => {},
            createEventSource: () => (callCount++ === 0 ? es1 : es2),
        });
        // Trigger error to schedule reconnect
        es1.fireError();
        // disconnect before timeout fires
        conn.disconnect();
        vi.runAllTimers();
        // es2 should NOT have been created (timeout was cancelled)
        expect(callCount).toBe(1);
    });

    it("calls onError with extracted info when connection errors", () => {
        const errors: SSEErrorInfo[] = [];
        const { es } = makeConnection({
            onError: (info) => errors.push(info),
        });
        es.fireError(Object.assign(new Event("error"), { status: 503 }));
        expect(errors).toHaveLength(1);
        expect(errors[0]?.status).toBe(503);
    });

    it("onError is optional — no crash when not provided", () => {
        const { es } = makeConnection();
        expect(() => {
            es.fireError();
        }).not.toThrow();
    });

    it("schedules reconnect on error", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        connect({
            path: () => "/test",
            eventTypes: [],
            onEvent: () => {},
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

    it("caps reconnect delay at MAX_RECONNECT_DELAY", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        connect({
            path: () => "/test",
            eventTypes: [],
            onEvent: () => {},
            createEventSource: () => {
                const es = new FakeEventSource();
                esList.push(es);
                return es;
            },
        });
        // Trigger many errors to exhaust exponential backoff ceiling
        for (let i = 0; i < 10; i++) {
            esList[esList.length - 1]?.fireError();
            vi.advanceTimersByTime(60000);
        }
        expect(esList.length).toBeGreaterThan(1);
    });

    it("handles createEventSource throwing by calling onError and scheduling reconnect", () => {
        vi.useFakeTimers();
        const errors: SSEErrorInfo[] = [];
        let callCount = 0;
        connect({
            path: () => "/test",
            eventTypes: [],
            onEvent: () => {},
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
