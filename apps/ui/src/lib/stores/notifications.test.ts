// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { createNotificationStore, type Notification } from "./notifications.svelte";

function makeNotification(overrides: Partial<Notification> = {}): Notification {
    const base: Notification = {
        id: "01H000000000000000000NEW00",
        fingerprint: "fp",
        kind: "run.failed",
        severity: "error",
        task_name: "backup-db",
        run_id: "run-1",
        title: "backup-db failed",
        body: "exit 1",
        count: 1,
        occurrences: ["2026-05-05T12:00:00.000Z"],
        created_at: "2026-05-05T12:00:00.000Z",
        last_occurred_at: "2026-05-05T12:00:00.000Z",
    };
    return { ...base, ...overrides };
}

// FakeEventSource extends the platform EventTarget so addEventListener +
// dispatchEvent come for free. `fire(type, payload)` mimics the server pushing
// a named SSE message; the store's onEvent handler receives it as a real
// MessageEvent.
class FakeEventSource extends EventTarget {
    fire(eventType: string, payload: unknown): void {
        this.dispatchEvent(new MessageEvent(eventType, { data: JSON.stringify(payload) }));
    }
}

interface Harness {
    store: ReturnType<typeof createNotificationStore>;
    es: FakeEventSource;
}

function setupHarness(opts: {
    unread?: number;
    lastReadAt?: string;
    items?: Notification[];
}): Harness {
    const items = opts.items ?? [];
    const unread = opts.unread ?? 0;
    const lastReadAt = opts.lastReadAt;

    const fakeFetch: typeof fetch = (input) => {
        const url =
            typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        if (url.includes("/api/notifications/unread-count")) {
            return Promise.resolve(
                new Response(JSON.stringify({ count: unread, last_read_at: lastReadAt }), {
                    status: 200,
                    headers: { "content-type": "application/json" },
                }),
            );
        }
        if (url.includes("/api/notifications/read")) {
            return Promise.resolve(new Response(null, { status: 204 }));
        }
        if (url.includes("/api/notifications")) {
            return Promise.resolve(
                new Response(JSON.stringify({ items, next_cursor: undefined }), {
                    status: 200,
                    headers: { "content-type": "application/json" },
                }),
            );
        }
        return Promise.reject(new Error(`unexpected fetch ${url}`));
    };

    const fakeES = new FakeEventSource();
    const store = createNotificationStore({
        fetch: fakeFetch,
        // FakeEventSource extends EventTarget; the structural EventSource type
        // requires extra read-only fields that don't matter for these tests.
        // eslint-disable-next-line no-restricted-syntax
        createEventSource: () => fakeES as unknown as EventSource,
        getApiUrl: () => "http://test",
    });
    return { store, es: fakeES };
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

    it("ignores SSE events that predate the last-read marker", async () => {
        const { store, es } = setupHarness({
            items: [],
            unread: 0,
            lastReadAt: "2026-05-05T12:00:00.000Z",
        });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({
                id: "01H000000000000000000OLD00",
                last_occurred_at: "2026-05-05T11:59:00.000Z",
            }),
        });
        expect(store.unread).toBe(0);
        expect(store.items).toHaveLength(1);
    });

    it("bumps unread when a created event postdates the marker", async () => {
        const { store, es } = setupHarness({
            items: [],
            unread: 0,
            lastReadAt: "2026-05-05T12:00:00.000Z",
        });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({
                id: "01H000000000000000000NEW01",
                last_occurred_at: "2026-05-05T12:01:00.000Z",
            }),
        });
        expect(store.unread).toBe(1);
    });

    it("bumps unread by count delta on a coalesced update", async () => {
        const id = "01H000000000000000000UPD01";
        const { store, es } = setupHarness({
            items: [],
            unread: 0,
            lastReadAt: "2026-05-05T12:00:00.000Z",
        });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({
                id,
                count: 1,
                last_occurred_at: "2026-05-05T12:01:00.000Z",
            }),
        });
        expect(store.unread).toBe(1);
        es.fire("notification.updated", {
            notification: makeNotification({
                id,
                count: 4,
                last_occurred_at: "2026-05-05T12:02:00.000Z",
            }),
        });
        expect(store.unread).toBe(4);
        expect(store.items).toHaveLength(1);
        expect(store.items[0]?.count).toBe(4);
    });

    it("does not bump on update when last_occurred_at predates the marker", async () => {
        const id = "01H000000000000000000UPD02";
        const { store, es } = setupHarness({
            items: [],
            unread: 0,
            lastReadAt: "2026-05-05T13:00:00.000Z",
        });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({
                id,
                count: 1,
                last_occurred_at: "2026-05-05T12:00:00.000Z",
            }),
        });
        expect(store.unread).toBe(0);
        es.fire("notification.updated", {
            notification: makeNotification({
                id,
                count: 5,
                last_occurred_at: "2026-05-05T12:30:00.000Z",
            }),
        });
        expect(store.unread).toBe(0);
    });

    it("clears unread on markAllRead()", async () => {
        const { store } = setupHarness({ items: [makeNotification()], unread: 5 });
        await store.init();
        expect(store.unread).toBe(5);
        await store.markAllRead();
        expect(store.unread).toBe(0);
        expect(store.lastReadAt).not.toBeNull();
    });
});
