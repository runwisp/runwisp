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
