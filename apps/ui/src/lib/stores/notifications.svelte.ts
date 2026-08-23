// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { z } from "zod";
import { getApiUrl as defaultGetApiUrl } from "$lib/utils/env";
import type { AppEventStream } from "./event-manager";
import { appEventStream } from "./app-stream.svelte";
import { createLogger } from "$lib/utils/logger";
import { connectionStore } from "./connection.svelte";

const notificationSchema = z.object({
    id: z.string(),
    fingerprint: z.string(),
    kind: z.string(),
    severity: z.enum(["info", "warn", "error"]),
    taskName: z.string().default(""),
    runId: z.string().default(""),
    title: z.string().default(""),
    body: z.string().default(""),
    count: z.number().int().nonnegative(),
    occurrences: z.array(z.string()).default([]),
    createdAt: z.string(),
    lastOccurredAt: z.string(),
    readAt: z.string().nullable().optional(),
});

export type Notification = z.infer<typeof notificationSchema>;

const listResponseSchema = z.object({
    items: z.array(notificationSchema),
    nextCursor: z.string().optional(),
});

const unreadResponseSchema = z.object({
    count: z.number().int().nonnegative(),
});

const unreadCountChangedSchema = z.object({
    unreadCount: z.number().int().nonnegative(),
});

const streamEnvelopeSchema = z.object({
    notification: notificationSchema,
    unreadCount: z.number().int().nonnegative(),
});

export interface NotificationStoreDeps {
    fetch?: typeof fetch;
    /** The shared app-event stream to ride. Defaults to the singleton; tests
     * pass an EventManager wired to a fake EventSource. */
    events?: AppEventStream;
    getApiUrl?: () => string;
}

const PAGE_SIZE = 50;
const SOURCE_ID = "notifications";

function isUnread(n: Notification): boolean {
    return !n.readAt;
}

class NotificationStore {
    #items = $state<Notification[]>([]);
    // unread is the server's authoritative snapshot. Every SSE event
    // (notification.created/updated/unreadCountChanged) carries the
    // post-mutation count and we set it directly from that — never delta
    // math, since a row absent from our paginated #items would otherwise
    // drift the count on every recurrence. Local mark-read/unread actions
    // apply an optimistic delta until the server round-trip (and its own
    // SSE echo) resolves it. We never recompute it from #items because
    // items is paginated.
    #unread = $state(0);
    readonly #events: AppEventStream;
    #subscribed = false;
    #unsubscribes: (() => void)[] = [];
    #connected = $state(false);
    #loaded = $state(false);
    #initInFlight: Promise<void> | null = null;
    #cursor: string | null = null;
    #hasMore = $state(false);
    readonly #logger = createLogger("NotificationStore");

    readonly #fetch: typeof fetch;
    readonly #getApiUrl: () => string;

    constructor(deps: Required<NotificationStoreDeps>) {
        this.#fetch = deps.fetch;
        this.#getApiUrl = deps.getApiUrl;
        this.#events = deps.events;
    }

    get items(): Notification[] {
        return this.#items;
    }
    get unread(): number {
        return this.#unread;
    }
    get connected(): boolean {
        return this.#connected;
    }
    get loaded(): boolean {
        return this.#loaded;
    }
    get hasMore(): boolean {
        return this.#hasMore;
    }

    /** Fetch the first page and start streaming. Idempotent and re-entrancy-safe:
     * concurrent callers all observe the same in-flight Promise. */
    init(): Promise<void> {
        if (this.#loaded) {
            this.#connect();
            return Promise.resolve();
        }
        if (this.#initInFlight) return this.#initInFlight;
        this.#initInFlight = this.#runInit().finally(() => {
            this.#initInFlight = null;
        });
        return this.#initInFlight;
    }

    async #runInit(): Promise<void> {
        try {
            const page = await this.#fetchPage();
            this.#items = [...page.items];
            this.#cursor = page.nextCursor ?? null;
            this.#hasMore = Boolean(page.nextCursor);
            this.#unread = await this.#fetchUnread();
            this.#loaded = true;
            this.#connect();
        } catch (e) {
            this.#logger.error("Failed to initialize notifications", e);
        }
    }

    /** Load the next page of older notifications, if any. */
    async loadMore(): Promise<void> {
        if (!this.#cursor) return;
        try {
            const page = await this.#fetchPage(this.#cursor);
            for (const n of page.items) {
                if (this.#items.some((x) => x.id === n.id)) continue;
                this.#items.push(n);
            }
            // The server returns DESC by id; appending preserves the contract.
            this.#cursor = page.nextCursor ?? null;
            this.#hasMore = Boolean(page.nextCursor);
        } catch (e) {
            this.#logger.error("Failed to load more notifications", e);
        }
    }

    /** Stamp every currently-unread row read on the server, and apply the
     * change locally to the loaded slice. */
    async markAllRead(): Promise<void> {
        const now = new Date().toISOString();
        try {
            const res = await this.#fetch(`${this.#getApiUrl()}/api/notifications/read`, {
                method: "POST",
                credentials: "include",
            });
            if (!res.ok) throw new Error(`Mark-read returned ${res.status.toString()}`);
            this.#items = this.#items.map((n) => (isUnread(n) ? { ...n, readAt: now } : n));
            this.#unread = 0;
        } catch (e) {
            this.#logger.error("Failed to mark notifications read", e);
        }
    }

    /** Mark a single notification read. */
    async markRead(id: string): Promise<void> {
        await this.#setReadState(id, true);
    }

    /** Mark a single notification unread. */
    async markUnread(id: string): Promise<void> {
        await this.#setReadState(id, false);
    }

    async #setReadState(id: string, read: boolean): Promise<void> {
        const idx = this.#items.findIndex((n) => n.id === id);
        const previous = this.#items[idx];
        if (!previous) return;
        const wasUnread = isUnread(previous);
        if (read === !wasUnread) return;

        const optimistic: Notification = {
            ...previous,
            readAt: read ? new Date().toISOString() : null,
        };
        const next = this.#items.slice();
        next[idx] = optimistic;
        this.#items = next;
        this.#unread = Math.max(0, this.#unread + (read ? -1 : 1));

        const verb = read ? "read" : "unread";
        try {
            const url = `${this.#getApiUrl()}/api/notifications/${encodeURIComponent(id)}/${verb}`;
            const res = await this.#fetch(url, {
                method: "POST",
                credentials: "include",
            });
            if (!res.ok) throw new Error(`Mark-${verb} returned ${res.status.toString()}`);
        } catch (e) {
            this.#logger.error(`Failed to mark notification ${verb}`, e);
            // Roll back the optimistic change.
            const rollback = this.#items.slice();
            const j = rollback.findIndex((n) => n.id === id);
            if (j >= 0) {
                rollback[j] = previous;
                this.#items = rollback;
                this.#unread = Math.max(0, this.#unread + (read ? 1 : -1));
            }
        }
    }

    disconnect(): void {
        for (const off of this.#unsubscribes) off();
        this.#unsubscribes = [];
        this.#subscribed = false;
        this.#connected = false;
        connectionStore.reportSourceDown(SOURCE_ID);
    }

    #connect(): void {
        if (this.#subscribed) return;
        this.#subscribed = true;
        this.#unsubscribes.push(
            this.#events.onOpen(() => {
                this.#connected = true;
                connectionStore.reportSourceUp(SOURCE_ID);
            }),
            this.#events.onError((info) => {
                this.#connected = false;
                if (info.status !== 401) {
                    connectionStore.reportSourceDown(
                        SOURCE_ID,
                        info.message ?? "Notifications stream error",
                    );
                }
            }),
            this.#events.onStall(() => {
                this.#connected = false;
                connectionStore.reportSourceStalled(SOURCE_ID);
            }),
        );
        const onNotification = (eventType: string) => (data: string) => {
            try {
                this.#logger.debug("SSE notification event", eventType);
                const raw: unknown = JSON.parse(data);
                const parsed = streamEnvelopeSchema.safeParse(raw);
                if (!parsed.success) {
                    this.#logger.warn("Invalid notification SSE payload", parsed.error.message);
                    return;
                }
                this.#applyUpdate(parsed.data.notification);
                this.#unread = parsed.data.unreadCount;
            } catch (e) {
                this.#logger.error("Malformed notification SSE event", e);
            }
        };
        this.#unsubscribes.push(
            this.#events.subscribe("notification.created", onNotification("notification.created")),
            this.#events.subscribe("notification.updated", onNotification("notification.updated")),
            this.#events.subscribe("notification.unreadCountChanged", (data: string) => {
                try {
                    const raw: unknown = JSON.parse(data);
                    const parsed = unreadCountChangedSchema.safeParse(raw);
                    if (!parsed.success) {
                        this.#logger.warn("Invalid unread-count SSE payload", parsed.error.message);
                        return;
                    }
                    this.#unread = parsed.data.unreadCount;
                } catch (e) {
                    this.#logger.error("Malformed unread-count SSE event", e);
                }
            }),
        );
    }

    #applyUpdate(n: Notification): void {
        const idx = this.#items.findIndex((x) => x.id === n.id);
        if (idx !== -1) {
            const next = this.#items.slice();
            next[idx] = n;
            this.#items = next;
            return;
        }
        this.#items = [n, ...this.#items];
    }

    async #fetchPage(before?: string): Promise<z.infer<typeof listResponseSchema>> {
        let qs = `limit=${PAGE_SIZE.toString()}`;
        if (before) qs += `&before=${encodeURIComponent(before)}`;
        const url = `${this.#getApiUrl()}/api/notifications?${qs}`;
        const res = await this.#fetch(url, {
            credentials: "include",
        });
        if (!res.ok) throw new Error(`List returned ${res.status.toString()}`);
        const raw: unknown = await res.json();
        return listResponseSchema.parse(raw);
    }

    async #fetchUnread(): Promise<number> {
        const res = await this.#fetch(`${this.#getApiUrl()}/api/notifications/unreadCount`, {
            credentials: "include",
        });
        if (!res.ok) throw new Error(`Unread returned ${res.status.toString()}`);
        const raw: unknown = await res.json();
        return unreadResponseSchema.parse(raw).count;
    }
}

/** Construct a notification store. Tests pass `deps` to inject fakes; the
 * default singleton uses the browser-auth EventSource factory and global fetch. */
export function createNotificationStore(deps: NotificationStoreDeps = {}): NotificationStore {
    return new NotificationStore({
        fetch: deps.fetch ?? ((...args) => globalThis.fetch(...args)),
        events: deps.events ?? appEventStream,
        getApiUrl: deps.getApiUrl ?? defaultGetApiUrl,
    });
}

export const notificationStore = createNotificationStore();
