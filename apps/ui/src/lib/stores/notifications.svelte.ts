// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { z } from "zod";
import { browser } from "$app/environment";
import {
    browserAuthEventSourceFactory,
    browserTokenStorage,
    type EventSourceFactory,
} from "$lib/adapters/browser";
import { getApiUrl as defaultGetApiUrl } from "$lib/utils/env";
import { EventManager } from "./event-manager.svelte";
import { createLogger } from "$lib/utils/logger";
import { connectionStore } from "./connection.svelte";

function authHeaders(): Record<string, string> {
    if (!browser) return {};
    const token = browserTokenStorage.getToken();
    return token ? { Authorization: `Bearer ${token}` } : {};
}

const notificationSchema = z.object({
    id: z.string(),
    fingerprint: z.string(),
    kind: z.string(),
    severity: z.enum(["info", "warn", "error"]),
    task_name: z.string().default(""),
    run_id: z.string().default(""),
    title: z.string().default(""),
    body: z.string().default(""),
    count: z.number().int().nonnegative(),
    occurrences: z.array(z.string()).default([]),
    created_at: z.string(),
    last_occurred_at: z.string(),
    read_at: z.string().nullable().optional(),
});

export type Notification = z.infer<typeof notificationSchema>;

const listResponseSchema = z.object({
    items: z.array(notificationSchema),
    next_cursor: z.string().optional(),
});

const unreadResponseSchema = z.object({
    count: z.number().int().nonnegative(),
});

const streamEnvelopeSchema = z.object({ notification: notificationSchema });

export interface NotificationStoreDeps {
    fetch?: typeof fetch;
    createEventSource?: EventSourceFactory;
    getApiUrl?: () => string;
}

const PAGE_SIZE = 50;
const SOURCE_ID = "notifications";

function isUnread(n: Notification): boolean {
    return !n.read_at;
}

class NotificationStore {
    #items = $state<Notification[]>([]);
    // unread is the server's authoritative snapshot, adjusted by the deltas
    // we observe locally (SSE events + per-row mark-read/unread). We never
    // recompute it from #items because items is paginated.
    #unread = $state(0);
    readonly #events: EventManager;
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
        this.#events = new EventManager({
            path: "/api/notifications/stream",
            createEventSource: deps.createEventSource,
            getApiUrl: deps.getApiUrl,
        });
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
            this.#cursor = page.next_cursor ?? null;
            this.#hasMore = Boolean(page.next_cursor);
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
            this.#cursor = page.next_cursor ?? null;
            this.#hasMore = Boolean(page.next_cursor);
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
                headers: authHeaders(),
            });
            if (!res.ok) throw new Error(`Mark-read returned ${res.status.toString()}`);
            this.#items = this.#items.map((n) => (isUnread(n) ? { ...n, read_at: now } : n));
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
            read_at: read ? new Date().toISOString() : null,
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
                headers: authHeaders(),
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
            } catch (e) {
                this.#logger.error("Malformed notification SSE event", e);
            }
        };
        this.#unsubscribes.push(
            this.#events.subscribe("notification.created", onNotification("notification.created")),
            this.#events.subscribe("notification.updated", onNotification("notification.updated")),
        );
    }

    #applyUpdate(n: Notification): void {
        const idx = this.#items.findIndex((x) => x.id === n.id);
        const prev = this.#items[idx];
        if (prev) {
            const next = this.#items.slice();
            next[idx] = n;
            this.#items = next;
            const delta = (isUnread(n) ? 1 : 0) - (isUnread(prev) ? 1 : 0);
            if (delta !== 0) this.#unread = Math.max(0, this.#unread + delta);
            return;
        }
        this.#items = [n, ...this.#items];
        if (isUnread(n)) this.#unread += 1;
    }

    async #fetchPage(before?: string): Promise<z.infer<typeof listResponseSchema>> {
        let qs = `limit=${PAGE_SIZE.toString()}`;
        if (before) qs += `&before=${encodeURIComponent(before)}`;
        const url = `${this.#getApiUrl()}/api/notifications?${qs}`;
        const res = await this.#fetch(url, {
            credentials: "include",
            headers: authHeaders(),
        });
        if (!res.ok) throw new Error(`List returned ${res.status.toString()}`);
        const raw: unknown = await res.json();
        return listResponseSchema.parse(raw);
    }

    async #fetchUnread(): Promise<number> {
        const res = await this.#fetch(`${this.#getApiUrl()}/api/notifications/unread-count`, {
            credentials: "include",
            headers: authHeaders(),
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
        createEventSource: deps.createEventSource ?? browserAuthEventSourceFactory,
        getApiUrl: deps.getApiUrl ?? defaultGetApiUrl,
    });
}

export const notificationStore = createNotificationStore();
