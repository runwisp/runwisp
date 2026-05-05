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
        read_at: null,
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

interface RecordedRequest {
    url: string;
    method: string;
}

interface Harness {
    store: ReturnType<typeof createNotificationStore>;
    es: FakeEventSource;
    requests: RecordedRequest[];
}

function setupHarness(opts: { unread?: number; items?: Notification[] }): Harness {
    const items = opts.items ?? [];
    const unread = opts.unread ?? 0;
    const requests: RecordedRequest[] = [];

    const fakeFetch: typeof fetch = (input, init) => {
        const url =
            typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        const method =
            init?.method ?? (typeof input !== "string" && "method" in input ? input.method : "GET");
        requests.push({ url, method });
        if (url.includes("/api/notifications/unread-count")) {
            return Promise.resolve(
                new Response(JSON.stringify({ count: unread }), {
                    status: 200,
                    headers: { "content-type": "application/json" },
                }),
            );
        }
        if (url.match(/\/api\/notifications\/[^/]+\/(read|unread)$/)) {
            return Promise.resolve(new Response(null, { status: 204 }));
        }
        if (url.endsWith("/api/notifications/read")) {
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
    return { store, es: fakeES, requests };
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
                read_at: "2026-05-05T12:00:00.000Z",
            }),
        });
        expect(store.unread).toBe(0);
        expect(store.items).toHaveLength(1);
    });

    it("bumps unread when an unread row is created", async () => {
        const { store, es } = setupHarness({ items: [], unread: 0 });
        await store.init();
        es.fire("notification.created", {
            notification: makeNotification({ id: "01H000000000000000000NEW01" }),
        });
        expect(store.unread).toBe(1);
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
            notification: makeNotification({ id, read_at: "2026-05-05T12:05:00.000Z" }),
        });
        expect(store.unread).toBe(0);
        expect(store.items[0]?.read_at).toBe("2026-05-05T12:05:00.000Z");
    });

    it("re-bumps unread when a coalesce SSE update clears read_at", async () => {
        const id = "01H000000000000000000UPD02";
        const { store, es } = setupHarness({
            items: [makeNotification({ id, read_at: "2026-05-05T12:00:00.000Z" })],
            unread: 0,
        });
        await store.init();
        expect(store.unread).toBe(0);
        es.fire("notification.updated", {
            notification: makeNotification({ id, count: 2, read_at: null }),
        });
        expect(store.unread).toBe(1);
        expect(store.items[0]?.read_at).toBeNull();
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
        expect(store.items[0]?.read_at).not.toBeNull();
        const markCall = requests.find(
            (r) => r.url.endsWith("/api/notifications/read") && r.method === "POST",
        );
        expect(markCall).toBeDefined();
    });

    it("markRead() sets read_at locally and POSTs the per-row endpoint", async () => {
        const id = "01H000000000000000000ROW01";
        const { store, requests } = setupHarness({
            items: [makeNotification({ id })],
            unread: 1,
        });
        await store.init();
        await store.markRead(id);
        expect(store.items[0]?.read_at).not.toBeNull();
        expect(store.unread).toBe(0);
        const call = requests.find((r) => r.url.endsWith(`/api/notifications/${id}/read`));
        expect(call?.method).toBe("POST");
    });

    it("markUnread() clears read_at locally and POSTs the per-row endpoint", async () => {
        const id = "01H000000000000000000ROW02";
        const { store, requests } = setupHarness({
            items: [makeNotification({ id, read_at: "2026-05-05T12:00:00.000Z" })],
            unread: 0,
        });
        await store.init();
        await store.markUnread(id);
        expect(store.items[0]?.read_at).toBeNull();
        expect(store.unread).toBe(1);
        const call = requests.find((r) => r.url.endsWith(`/api/notifications/${id}/unread`));
        expect(call?.method).toBe("POST");
    });
});
