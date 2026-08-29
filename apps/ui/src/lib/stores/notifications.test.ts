// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { createNotificationStore, type Notification } from "./notifications.svelte";
import { EventManager } from "./event-manager";
import { connectionStore } from "./connection.svelte";
import type { SSEStream } from "$lib/adapters/browser";

function makeNotification(overrides: Partial<Notification> = {}): Notification {
    const base: Notification = {
        id: "01H000000000000000000NEW00",
        fingerprint: "fp",
        kind: "run.failed",
        severity: "error",
        taskName: "backup-db",
        runId: "run-1",
        title: "backup-db failed",
        body: "exit 1",
        count: 1,
        occurrences: ["2026-05-05T12:00:00.000Z"],
        createdAt: "2026-05-05T12:00:00.000Z",
        lastOccurredAt: "2026-05-05T12:00:00.000Z",
        readAt: null,
    };
    return { ...base, ...overrides };
}

// FakeEventSource is a typed SSEStream the EventManager treats as a real
// stream. It composes an internal EventTarget for dispatch and exposes only
// the surface EventManager touches (close, readyState, onopen, onerror,
// addEventListener). `fire(type, payload)` mimics the server pushing a named
// SSE message; the store's handler receives it as a real MessageEvent.
class FakeEventSource implements SSEStream {
    readyState = 1;
    onopen: ((ev: Event) => unknown) | null = null;
    onerror: ((ev: Event) => unknown) | null = null;
    onmessage: ((ev: MessageEvent) => unknown) | null = null;

    readonly #target = new EventTarget();

    close(): void {
        this.readyState = 2;
    }

    addEventListener(
        type: string,
        listener: (event: MessageEvent) => void,
        options?: boolean | AddEventListenerOptions,
    ): void {
        this.#target.addEventListener(
            type,
            (event: Event) => {
                if (event instanceof MessageEvent) listener(event);
            },
            options,
        );
    }

    /** Push a server-style named SSE event to bound listeners. */
    fire(eventType: string, payload: unknown): void {
        this.#target.dispatchEvent(new MessageEvent(eventType, { data: JSON.stringify(payload) }));
    }
}

interface RecordedRequest {
    url: string;
    method: string;
}

interface Harness {
    store: ReturnType<typeof createNotificationStore>;
    es: FakeEventSource;
    requests: RecordedRequest[];
    setUnread: (n: number) => void;
    setItems: (items: Notification[]) => void;
    /** Gate the /api/notifications/read response behind a caller-controlled promise. */
    setMarkAllReadGate: (gate: Promise<void>) => void;
    /** Force the per-row mark-read/unread POST to fail with a 500. */
    setRowActionFails: (fail: boolean) => void;
}

function setupHarness(opts: { unread?: number; items?: Notification[] }): Harness {
    let items = opts.items ?? [];
    let unread = opts.unread ?? 0;
    let markAllReadGate: Promise<void> = Promise.resolve();
    let rowActionFails = false;
    const requests: RecordedRequest[] = [];

    const fakeFetch: typeof fetch = (input, init) => {
        const url =
            typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        const method =
            init?.method ?? (typeof input !== "string" && "method" in input ? input.method : "GET");
        requests.push({ url, method });
        if (url.includes("/api/notifications/unreadCount")) {
            return Promise.resolve(
                new Response(JSON.stringify({ count: unread }), {
                    status: 200,
                    headers: { "content-type": "application/json" },
                }),
            );
        }
        if (url.match(/\/api\/notifications\/[^/]+\/(read|unread)$/)) {
            return Promise.resolve(new Response(null, { status: rowActionFails ? 500 : 204 }));
        }
        if (url.endsWith("/api/notifications/read")) {
            return markAllReadGate.then(() => new Response(null, { status: 204 }));
        }
        if (url.includes("/api/notifications")) {
            return Promise.resolve(
                new Response(JSON.stringify({ items, nextCursor: undefined }), {
                    status: 200,
                    headers: { "content-type": "application/json" },
                }),
            );
        }
        return Promise.reject(new Error(`unexpected fetch ${url}`));
    };

    const fakeES = new FakeEventSource();
    const events = new EventManager({
        path: "/api/events/stream",
        createEventSource: () => fakeES,
        getApiUrl: () => "http://test",
    });
    const store = createNotificationStore({
        fetch: fakeFetch,
        events,
        getApiUrl: () => "http://test",
    });
    return {
        store,
        es: fakeES,
        requests,
        setUnread: (n) => {
            unread = n;
        },
        setItems: (next) => {
            items = next;
        },
        setMarkAllReadGate: (gate) => {
            markAllReadGate = gate;
        },
        setRowActionFails: (fail) => {
            rowActionFails = fail;
        },
    };
}

describe("NotificationStore", () => {
    it("seeds items and unread count from the server during init()", async () => {
        const seed = makeNotification({ id: "01H000000000000000000SEED1" });
        const { store } = setupHarness({ items: [seed], unread: 3 });
        await store.init();
        expect(store.items).toHaveLength(1);
        expect(store.items[0]?.id).toBe(seed.id);
        expect(store.unread).toBe(3);
        expect(store.loaded).toBe(true);
    });

    it("returns the same in-flight Promise to concurrent init() callers", async () => {
        const { store } = setupHarness({ items: [], unread: 0 });
        const a = store.init();
        const b = store.init();
        expect(a).toBe(b);
        await a;
    });

    it("ignores SSE 'updated' events whose row already counted as read", async () => {
        const { store, es } = setupHarness({ items: [], unread: 0 });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({
                id: "01H000000000000000000RD000",
                readAt: "2026-05-05T12:00:00.000Z",
            }),
            unreadCount: 0,
        });
        expect(store.unread).toBe(0);
        expect(store.items).toHaveLength(1);
    });

    it("bumps unread when an unread row is created", async () => {
        const { store, es } = setupHarness({ items: [], unread: 0 });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({ id: "01H000000000000000000NEW01" }),
            unreadCount: 1,
        });
        expect(store.unread).toBe(1);
    });

    it("sets unread directly from the unreadCount on the envelope, ignoring #items entirely, for a recurring notification not loaded in this tab", async () => {
        const { store, es } = setupHarness({ items: [], unread: 5 });
        await store.init();
        expect(store.unread).toBe(5);
        // Simulate a coalesced/recurring failure re-firing as `notification.updated`
        // whose row has scrolled off this tab's loaded page (or the tab opened
        // after it was first created): absent from #items, but the server ships
        // the authoritative post-mutation count alongside it. Old delta math would
        // have unconditionally bumped #unread to 6 here; the fix must land on the
        // exact server value instead.
        es.fire("notification.updated", {
            notification: makeNotification({
                id: "01H000000000000000000OFF01",
                count: 4,
                readAt: null,
            }),
            unreadCount: 9,
        });
        expect(store.unread).toBe(9);
    });

    it("sets unread directly from a notification.unreadCountChanged SSE event", async () => {
        const { store, es } = setupHarness({ items: [], unread: 0 });
        await store.init();
        es.fire("notification.unreadCountChanged", { unreadCount: 7 });
        expect(store.unread).toBe(7);
    });

    it("decrements unread when SSE update flips a row from unread to read", async () => {
        const id = "01H000000000000000000UPD01";
        const { store, es } = setupHarness({
            items: [makeNotification({ id })],
            unread: 1,
        });
        await store.init();
        expect(store.unread).toBe(1);
        es.fire("notification.updated", {
            notification: makeNotification({ id, readAt: "2026-05-05T12:05:00.000Z" }),
            unreadCount: 0,
        });
        expect(store.unread).toBe(0);
        expect(store.items[0]?.readAt).toBe("2026-05-05T12:05:00.000Z");
    });

    it("re-bumps unread when a coalesce SSE update clears readAt", async () => {
        const id = "01H000000000000000000UPD02";
        const { store, es } = setupHarness({
            items: [makeNotification({ id, readAt: "2026-05-05T12:00:00.000Z" })],
            unread: 0,
        });
        await store.init();
        expect(store.unread).toBe(0);
        es.fire("notification.updated", {
            notification: makeNotification({ id, count: 2, readAt: null }),
            unreadCount: 1,
        });
        expect(store.unread).toBe(1);
        expect(store.items[0]?.readAt).toBeNull();
    });

    it("clears unread on markAllRead() and stamps loaded items", async () => {
        const id = "01H000000000000000000ALL01";
        const { store, requests } = setupHarness({
            items: [makeNotification({ id })],
            unread: 5,
        });
        await store.init();
        expect(store.unread).toBe(5);
        await store.markAllRead();
        expect(store.unread).toBe(0);
        expect(store.items[0]?.readAt).not.toBeNull();
        const markCall = requests.find(
            (r) => r.url.endsWith("/api/notifications/read") && r.method === "POST",
        );
        expect(markCall).toBeDefined();
    });

    it("markRead() sets readAt locally and POSTs the per-row endpoint", async () => {
        const id = "01H000000000000000000ROW01";
        const { store, requests } = setupHarness({
            items: [makeNotification({ id })],
            unread: 1,
        });
        await store.init();
        await store.markRead(id);
        expect(store.items[0]?.readAt).not.toBeNull();
        expect(store.unread).toBe(0);
        const call = requests.find((r) => r.url.endsWith(`/api/notifications/${id}/read`));
        expect(call?.method).toBe("POST");
    });

    it("markUnread() clears readAt locally and POSTs the per-row endpoint", async () => {
        const id = "01H000000000000000000ROW02";
        const { store, requests } = setupHarness({
            items: [makeNotification({ id, readAt: "2026-05-05T12:00:00.000Z" })],
            unread: 0,
        });
        await store.init();
        await store.markUnread(id);
        expect(store.items[0]?.readAt).toBeNull();
        expect(store.unread).toBe(1);
        const call = requests.find((r) => r.url.endsWith(`/api/notifications/${id}/unread`));
        expect(call?.method).toBe("POST");
    });

    it("does not sweep a notification created while markAllRead() is still in flight", async () => {
        const existingId = "01H000000000000000000EXIST1";
        const { store, es, setMarkAllReadGate } = setupHarness({
            items: [makeNotification({ id: existingId })],
            unread: 1,
        });
        await store.init();

        let releaseGate: () => void = () => {
            throw new Error("gate resolver not assigned");
        };
        setMarkAllReadGate(
            new Promise<void>((resolve) => {
                releaseGate = resolve;
            }),
        );

        const markAllReadPromise = store.markAllRead();

        // A brand-new notification is created (and delivered via its own SSE
        // event, with its own authoritative count) while the mark-all-read
        // POST is still pending.
        const newId = "01H000000000000000000NEWCR1";
        es.fire("notification.created", {
            notification: makeNotification({ id: newId }),
            unreadCount: 2,
        });
        expect(store.unread).toBe(2);

        releaseGate();
        await markAllReadPromise;

        expect(store.items.find((n) => n.id === existingId)?.readAt).not.toBeNull();
        expect(store.items.find((n) => n.id === newId)?.readAt).toBeNull();
        expect(store.unread).toBe(1);
    });

    it("on markRead() failure, rolls back only readAt and keeps a newer SSE update", async () => {
        const id = "01H000000000000000000RB0001";
        const { store, es, setRowActionFails } = setupHarness({
            items: [makeNotification({ id, count: 1 })],
            unread: 1,
        });
        await store.init();
        setRowActionFails(true);

        const markReadPromise = store.markRead(id);

        // The row is coalesced again (bumped count) while the failing POST is
        // still in flight.
        es.fire("notification.updated", {
            notification: makeNotification({ id, count: 5, readAt: null }),
            unreadCount: 1,
        });
        expect(store.items[0]?.count).toBe(5);

        await markReadPromise;

        // Rolled back to unread, but the SSE-delivered count bump survives —
        // the old rollback restored the whole stale `previous` snapshot,
        // which would have reverted count back to 1 here.
        expect(store.items[0]?.readAt).toBeNull();
        expect(store.items[0]?.count).toBe(5);
        // The SSE update already set unread to its authoritative value (1)
        // before the POST failed. The rollback must not layer another delta
        // on top of that (the old code computed max(0, 1 + 1) = 2) — it
        // re-fetches the authoritative count instead.
        expect(store.unread).toBe(1);
    });

    it("resyncs items and unread count when connectionStore reports a reconnect", async () => {
        const existingId = "01H000000000000000000RES001";
        const { store, setItems, setUnread } = setupHarness({
            items: [makeNotification({ id: existingId })],
            unread: 1,
        });
        await store.init();
        expect(store.unread).toBe(1);

        // Prime the singleton: markConnected() only treats a later call as a
        // recovery (and fires reconnect listeners) once it has a prior
        // successful connection to recover from.
        connectionStore.markConnected();

        // Server state moved on while this tab was disconnected (or a
        // cross-tab leader handoff dropped events): a differently-shaped page
        // is waiting behind the resync fetch.
        const freshId = "01H000000000000000000RES002";
        setItems([makeNotification({ id: freshId })]);
        setUnread(9);

        connectionStore.markDisconnected(new Error("test gap"));
        connectionStore.markConnected();
        // Flush the async #resync() fetch chain the reconnect listener kicked off.
        await new Promise((resolve) => setTimeout(resolve, 0));

        expect(store.unread).toBe(9);
        expect(store.items).toHaveLength(1);
        expect(store.items[0]?.id).toBe(freshId);
    });
});
