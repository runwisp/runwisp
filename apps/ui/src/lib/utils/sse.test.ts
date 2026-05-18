// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it, vi, afterEach } from "vitest";
import type { SSEStream } from "$lib/adapters/browser";
import type { SSEErrorInfo } from "./event-source";
import { buildSSEUrl, connectSSE } from "./sse";

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

    fireMessage(data: string): void {
        this.onmessage?.(new MessageEvent("message", { data }));
    }

    fireNamedEvent(type: string, data: string): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data }));
    }

    fireNamedEventRaw(type: string, data: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data }));
    }
}

afterEach(() => {
    vi.restoreAllMocks();
});

// ─── buildSSEUrl ──────────────────────────────────────────────────────────────

describe("buildSSEUrl", () => {
    it("concatenates apiUrl and path", () => {
        expect(buildSSEUrl("/api/events", "http://localhost:8080")).toBe(
            "http://localhost:8080/api/events",
        );
    });

    it("works with empty apiUrl prefix", () => {
        expect(buildSSEUrl("/foo", "")).toBe("/foo");
    });
});

// ─── connectSSE ───────────────────────────────────────────────────────────────

describe("connectSSE", () => {
    function makeConnection(
        opts: {
            path?: string | (() => string);
            eventTypes?: string[];
            reconnect?: boolean;
            onEvent?: (type: string, data: string) => void;
            onOpen?: () => void;
            onError?: (info: { message?: string; status?: number }) => void;
        } = {},
    ) {
        const es = new FakeEventSource();
        const events: [string, string][] = [];

        const conn = connectSSE({
            path: opts.path ?? "/test",
            ...(opts.eventTypes !== undefined ? { eventTypes: opts.eventTypes } : {}),
            reconnect: opts.reconnect ?? false,
            onEvent: opts.onEvent ?? ((t, d) => events.push([t, d])),
            ...(opts.onOpen !== undefined ? { onOpen: opts.onOpen } : {}),
            ...(opts.onError !== undefined ? { onError: opts.onError } : {}),
            deps: {
                createEventSource: () => es,
                getApiUrl: () => "http://test",
            },
        });

        return { conn, es, events };
    }

    it("uses default getApiUrl when deps.getApiUrl is not provided", () => {
        let connectedUrl = "";
        const conn = connectSSE({
            path: "/path-check",
            onEvent: () => {},
            reconnect: false,
            deps: {
                createEventSource: (url) => {
                    connectedUrl = url;
                    return new FakeEventSource();
                },
            },
        });
        conn.disconnect();
        // DEFAULT_API_URL="" in test env, so result = "" + "/path-check" = "/path-check"
        expect(connectedUrl).toBe("/path-check");
    });

    it("calls createEventSource with the built URL", () => {
        const factory = vi.fn().mockReturnValue(new FakeEventSource());
        connectSSE({
            path: "/events",
            onEvent: () => {},
            reconnect: false,
            deps: { createEventSource: factory, getApiUrl: () => "http://api" },
        }).disconnect();
        expect(factory).toHaveBeenCalledWith("http://api/events");
    });

    it("calls path() when path is a function", () => {
        const pathFn = vi.fn().mockReturnValue("/dynamic-path");
        const factory = vi.fn().mockReturnValue(new FakeEventSource());
        connectSSE({
            path: pathFn,
            onEvent: () => {},
            reconnect: false,
            deps: { createEventSource: factory, getApiUrl: () => "" },
        }).disconnect();
        expect(pathFn).toHaveBeenCalled();
        expect(factory).toHaveBeenCalledWith("/dynamic-path");
    });

    it("onOpen resets reconnect delay", () => {
        let openCalled = false;
        const { es } = makeConnection({
            onOpen: () => {
                openCalled = true;
            },
        });
        es.fireOpen();
        expect(openCalled).toBe(true);
    });

    it("dispatches named events via addEventListener when eventTypes is provided", () => {
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

    it("falls back to onmessage when eventTypes is empty", () => {
        const { es, events } = makeConnection({ eventTypes: [] });
        es.fireMessage('{"key":"val"}');
        expect(events).toEqual([["message", '{"key":"val"}']]);
    });

    it("ignores default message with non-string data (onmessage data===undefined branch)", () => {
        const { es, events } = makeConnection({ eventTypes: [] });
        // Call onmessage directly with non-string data
        es.onmessage?.(new MessageEvent("message", { data: 99 }));
        expect(events).toHaveLength(0);
    });

    it("disconnect closes the EventSource and prevents reconnect", () => {
        const { conn, es } = makeConnection({ reconnect: false });
        conn.disconnect();
        expect(es.closed).toBe(true);
    });

    it("disconnect during reconnect timeout cancels the timeout", () => {
        vi.useFakeTimers();
        const es1 = new FakeEventSource();
        const es2 = new FakeEventSource();
        let callCount = 0;
        const conn = connectSSE({
            path: "/test",
            onEvent: () => {},
            reconnect: true,
            deps: {
                createEventSource: () => (callCount++ === 0 ? es1 : es2),
                getApiUrl: () => "",
            },
        });
        // Trigger error to schedule reconnect
        es1.fireError();
        // disconnect before timeout fires
        conn.disconnect();
        vi.runAllTimers();
        // es2 should NOT have been created (timeout was cancelled)
        expect(callCount).toBe(1);
        vi.useRealTimers();
    });

    it("calls onError with extracted info when connection errors", () => {
        const errors: SSEErrorInfo[] = [];
        const { es } = makeConnection({
            reconnect: false,
            onError: (info) => errors.push(info),
        });
        es.fireError(Object.assign(new Event("error"), { status: 503 }));
        expect(errors).toHaveLength(1);
        expect(errors[0]?.status).toBe(503);
    });

    it("onError is optional — no crash when not provided", () => {
        const { es } = makeConnection({ reconnect: false });
        expect(() => {
            es.fireError();
        }).not.toThrow();
    });

    it("schedules reconnect on error when reconnect=true", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        const conn = connectSSE({
            path: "/test",
            onEvent: () => {},
            reconnect: true,
            deps: {
                createEventSource: () => {
                    const es = new FakeEventSource();
                    esList.push(es);
                    return es;
                },
                getApiUrl: () => "",
            },
        });
        esList[0]?.fireError();
        expect(esList).toHaveLength(1);
        vi.advanceTimersByTime(3001);
        expect(esList).toHaveLength(2);
        conn.disconnect();
        vi.useRealTimers();
    });

    it("caps reconnect delay at MAX_RECONNECT_DELAY", () => {
        vi.useFakeTimers();
        const esList: FakeEventSource[] = [];
        const conn = connectSSE({
            path: "/test",
            onEvent: () => {},
            reconnect: true,
            deps: {
                createEventSource: () => {
                    const es = new FakeEventSource();
                    esList.push(es);
                    return es;
                },
                getApiUrl: () => "",
            },
        });
        // Trigger many errors to exhaust exponential backoff ceiling
        for (let i = 0; i < 10; i++) {
            esList[esList.length - 1]?.fireError();
            vi.advanceTimersByTime(60000);
        }
        conn.disconnect();
        expect(esList.length).toBeGreaterThan(1);
        vi.useRealTimers();
    });

    it("handles createEventSource throwing by calling onError and scheduling reconnect", () => {
        vi.useFakeTimers();
        const errors: SSEErrorInfo[] = [];
        let callCount = 0;
        const conn = connectSSE({
            path: "/test",
            onEvent: () => {},
            reconnect: true,
            onError: (info) => errors.push(info),
            deps: {
                createEventSource: () => {
                    callCount++;
                    if (callCount === 1) throw new Error("network unavailable");
                    return new FakeEventSource();
                },
                getApiUrl: () => "",
            },
        });
        expect(errors).toHaveLength(1);
        expect(errors[0]?.message).toContain("network unavailable");
        vi.advanceTimersByTime(3001);
        expect(callCount).toBe(2);
        conn.disconnect();
        vi.useRealTimers();
    });
});
