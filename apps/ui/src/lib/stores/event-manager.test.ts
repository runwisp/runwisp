// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventManager } from "./event-manager";
import { SSE_CONFIG } from "$lib/config/constants";
import type { SSEStream } from "$lib/adapters/browser";

// A fake EventSource that fires nothing on its own — the test decides when
// `onopen`/`onerror` happen, so we can exercise the "created but never opened"
// stall window.
class ControllableEventSource implements SSEStream {
    readyState = 0;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;
    closed = false;

    close(): void {
        this.closed = true;
        this.readyState = 2;
    }

    addEventListener(): void {
        // no-op: stall detection never depends on bound event types
    }

    open(): void {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
    }

    error(): void {
        // Attach a message so error extraction doesn't reach `instanceof
        // ErrorEvent` (that global is absent in the node test env).
        this.onerror?.(Object.assign(new Event("error"), { message: "test error" }));
    }
}

describe("EventManager stall detection", () => {
    beforeEach(() => {
        vi.useFakeTimers();
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    function setup() {
        const es = new ControllableEventSource();
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => es,
            getApiUrl: () => "http://test",
        });
        return { es, mgr };
    }

    it("fires onStall when a connect attempt neither opens nor errors in time", () => {
        const { mgr } = setup();
        const onStall = vi.fn();
        const onOpen = vi.fn();
        mgr.onStall(onStall);
        mgr.onOpen(onOpen);

        mgr.subscribe("system", () => {});
        expect(onStall).not.toHaveBeenCalled();

        vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT);
        expect(onStall).toHaveBeenCalledTimes(1);
        expect(onOpen).not.toHaveBeenCalled();
    });

    it("does not stall once the connection opens within the window", () => {
        const { es, mgr } = setup();
        const onStall = vi.fn();
        const onOpen = vi.fn();
        mgr.onStall(onStall);
        mgr.onOpen(onOpen);

        mgr.subscribe("system", () => {});
        es.open();
        vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT * 2);

        expect(onOpen).toHaveBeenCalledTimes(1);
        expect(onStall).not.toHaveBeenCalled();
    });

    it("clears the pending stall timer when the attempt errors", () => {
        const { es, mgr } = setup();
        const onStall = vi.fn();
        mgr.onStall(onStall);

        mgr.subscribe("system", () => {});
        es.error();
        // Up to (but not into) the reconnect, the original stall timer must have
        // been cancelled — an error is not a stall. (The later reconnect, which
        // also never opens, will legitimately stall; that's covered elsewhere.)
        vi.advanceTimersByTime(SSE_CONFIG.RECONNECT_DELAY - 1);

        expect(onStall).not.toHaveBeenCalled();
    });

    it("recovers: a stalled connection that later opens fires onOpen", () => {
        const { es, mgr } = setup();
        const onStall = vi.fn();
        const onOpen = vi.fn();
        mgr.onStall(onStall);
        mgr.onOpen(onOpen);

        mgr.subscribe("system", () => {});
        vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT);
        expect(onStall).toHaveBeenCalledTimes(1);

        // The browser frees a slot and the pending EventSource finally opens.
        es.open();
        expect(onOpen).toHaveBeenCalledTimes(1);
    });
});

// A richer fake that can deliver named events (via a real EventTarget) so we can
// exercise the dispatch path, not just the stall window.
class FakeEventSource implements SSEStream {
    readyState = 0;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;
    closed = false;
    readonly #target = new EventTarget();

    close(): void {
        this.closed = true;
        this.readyState = 2;
    }

    addEventListener(type: string, listener: (event: MessageEvent) => void): void {
        this.#target.addEventListener(type, (event: Event) => {
            if (event instanceof MessageEvent) listener(event);
        });
    }

    open(): void {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
    }

    error(details: { message?: string; status?: number } = { message: "boom" }): void {
        this.onerror?.(Object.assign(new Event("error"), details));
    }

    fire(type: string, payload: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(payload) }));
    }

    fireData(type: string, data: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(type, { data }));
    }
}

describe("EventManager subscription and dispatch", () => {
    beforeEach(() => {
        vi.useRealTimers();
    });

    function setup() {
        const es = new FakeEventSource();
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => es,
            getApiUrl: () => "http://test",
        });
        return { es, mgr };
    }

    it("delivers a fired event to every subscriber of that type", () => {
        const { es, mgr } = setup();
        const a: string[] = [];
        const b: string[] = [];
        mgr.subscribe("run.created", (d) => a.push(d));
        mgr.subscribe("run.created", (d) => b.push(d));

        es.open();
        es.fire("run.created", { id: "r1" });

        expect(a[0]).toContain("r1");
        expect(b[0]).toContain("r1");
    });

    it("binds a type subscribed after the connection is already open", () => {
        const { es, mgr } = setup();
        mgr.subscribe("system", () => {});
        es.open();

        const late: string[] = [];
        mgr.subscribe("run.created", (d) => late.push(d));
        es.fire("run.created", { id: "r2" });

        expect(late[0]).toContain("r2");
    });

    it("ignores a message event whose data is not a string", () => {
        const { es, mgr } = setup();
        const received: string[] = [];
        mgr.subscribe("run.created", (d) => received.push(d));
        es.open();

        es.fireData("run.created", 123);

        expect(received).toHaveLength(0);
    });

    it("a throwing handler does not stop the others", () => {
        const { es, mgr } = setup();
        const after = vi.fn();
        mgr.subscribe("run.created", () => {
            throw new Error("handler boom");
        });
        mgr.subscribe("run.created", after);
        es.open();

        es.fire("run.created", { id: "r3" });

        expect(after).toHaveBeenCalledTimes(1);
    });

    it("disconnects the EventSource when the last handler unsubscribes", () => {
        const { es, mgr } = setup();
        const unsub = mgr.subscribe("system", () => {});
        es.open();
        expect(es.closed).toBe(false);

        unsub();

        expect(es.closed).toBe(true);
    });

    it("keeps the connection while other handlers for the type remain", () => {
        const { es, mgr } = setup();
        const unsubA = mgr.subscribe("system", () => {});
        mgr.subscribe("system", () => {});
        es.open();

        unsubA();

        expect(es.closed).toBe(false);
    });

    it("close() tears everything down and ignores later subscriptions", () => {
        const { es, mgr } = setup();
        mgr.subscribe("system", () => {});
        es.open();

        mgr.close();
        expect(es.closed).toBe(true);

        // After close, subscribing must not open a new connection.
        let created = 0;
        const mgr2 = new EventManager({
            path: "/api/stream",
            createEventSource: () => {
                created++;
                return new FakeEventSource();
            },
            getApiUrl: () => "http://test",
        });
        mgr2.close();
        mgr2.subscribe("system", () => {});
        expect(created).toBe(0);
    });

    it("lifecycle unsubscribers detach their handlers", () => {
        const { es, mgr } = setup();
        const onOpen = vi.fn();
        const offOpen = mgr.onOpen(onOpen);
        mgr.onError(() => {})();
        mgr.onStall(() => {})();

        offOpen();
        mgr.subscribe("system", () => {});
        es.open();

        expect(onOpen).not.toHaveBeenCalled();
    });

    it("a throwing onOpen handler is caught and does not block others", () => {
        const { es, mgr } = setup();
        const second = vi.fn();
        mgr.onOpen(() => {
            throw new Error("open boom");
        });
        mgr.onOpen(second);

        mgr.subscribe("system", () => {});
        es.open();

        expect(second).toHaveBeenCalledTimes(1);
    });
});

describe("EventManager errors and reconnect", () => {
    beforeEach(() => {
        vi.useFakeTimers();
    });
    afterEach(() => {
        vi.useRealTimers();
    });

    it("notifies error with details and reconnects after a backoff", () => {
        const sources: FakeEventSource[] = [];
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => {
                const es = new FakeEventSource();
                sources.push(es);
                return es;
            },
            getApiUrl: () => "http://test",
        });
        const onError = vi.fn();
        mgr.onError(onError);

        mgr.subscribe("system", () => {});
        sources[0].open();
        sources[0].error({ message: "dropped", status: 503 });

        expect(onError).toHaveBeenCalledTimes(1);
        expect(onError.mock.calls[0][0]).toMatchObject({ message: "dropped", status: 503 });

        // No second source until the backoff elapses, then a fresh connect.
        expect(sources).toHaveLength(1);
        vi.advanceTimersByTime(SSE_CONFIG.RECONNECT_DELAY);
        expect(sources).toHaveLength(2);
    });

    it("a failure to create the EventSource notifies error and retries", () => {
        let calls = 0;
        const recovered = new FakeEventSource();
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => {
                calls++;
                if (calls === 1) throw new Error("construct failed");
                return recovered;
            },
            getApiUrl: () => "http://test",
        });
        const onError = vi.fn();
        mgr.onError(onError);

        mgr.subscribe("system", () => {});
        expect(onError).toHaveBeenCalledTimes(1);

        vi.advanceTimersByTime(SSE_CONFIG.RECONNECT_DELAY);
        expect(calls).toBe(2);
    });

    it("a throwing onError handler is caught", () => {
        const es = new FakeEventSource();
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => es,
            getApiUrl: () => "http://test",
        });
        const second = vi.fn();
        mgr.onError(() => {
            throw new Error("error-handler boom");
        });
        mgr.onError(second);

        mgr.subscribe("system", () => {});
        es.error();

        expect(second).toHaveBeenCalledTimes(1);
    });

    it("a throwing onStall handler is caught", () => {
        const es = new FakeEventSource();
        const mgr = new EventManager({
            path: "/api/stream",
            createEventSource: () => es,
            getApiUrl: () => "http://test",
        });
        const second = vi.fn();
        mgr.onStall(() => {
            throw new Error("stall-handler boom");
        });
        mgr.onStall(second);

        mgr.subscribe("system", () => {});
        vi.advanceTimersByTime(SSE_CONFIG.OPEN_TIMEOUT);

        expect(second).toHaveBeenCalledTimes(1);
    });
});
