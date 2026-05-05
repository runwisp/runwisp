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
import { connectSSE, type SSEConnection } from "$lib/utils/sse";
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
});

export type Notification = z.infer<typeof notificationSchema>;

const listResponseSchema = z.object({
    items: z.array(notificationSchema),
    next_cursor: z.string().optional(),
});

const unreadResponseSchema = z.object({
    count: z.number().int().nonnegative(),
    last_read_at: z.string().optional(),
});

const streamEnvelopeSchema = z.object({ notification: notificationSchema });

export interface NotificationStoreDeps {
    fetch?: typeof fetch;
    createEventSource?: EventSourceFactory;
    getApiUrl?: () => string;
}

const PAGE_SIZE = 50;

class NotificationStore {
    #items = $state<Notification[]>([]);
    #unread = $state(0);
    #lastReadAt = $state<string | null>(null);
    #lastReadAtMs = 0;
    #connection: SSEConnection | null = null;
    #connected = $state(false);
    #loaded = $state(false);
    #initInFlight: Promise<void> | null = null;
    #cursor: string | null = null;
    #hasMore = $state(false);
    #logger = createLogger("NotificationStore");

    #fetch: typeof fetch;
    #createEventSource: EventSourceFactory;
    #getApiUrl: () => string;

    constructor(deps: Required<NotificationStoreDeps>) {
        this.#fetch = deps.fetch;
        this.#createEventSource = deps.createEventSource;
        this.#getApiUrl = deps.getApiUrl;
    }

    get items(): Notification[] {
        return this.#items;
    }
    get unread(): number {
        return this.#unread;
    }
    get lastReadAt(): string | null {
        return this.#lastReadAt;
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
            const { count, lastReadAt } = await this.#fetchUnread();
            this.#unread = count;
            this.#setLastReadAt(lastReadAt);
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

    /** Persist the operator's last-read marker; resets the unread count. */
    async markAllRead(): Promise<void> {
        const now = new Date().toISOString();
        try {
            const res = await this.#fetch(`${this.#getApiUrl()}/api/notifications/read`, {
                method: "POST",
                credentials: "include",
                headers: { "content-type": "application/json", ...authHeaders() },
                body: JSON.stringify({ last_read_at: now }),
            });
            if (!res.ok) throw new Error(`Mark-read returned ${res.status.toString()}`);
            this.#setLastReadAt(now);
            this.#unread = 0;
        } catch (e) {
            this.#logger.error("Failed to mark notifications read", e);
        }
    }

    disconnect(): void {
        this.#connection?.disconnect();
        this.#connection = null;
        this.#connected = false;
    }

    #connect(): void {
        if (this.#connection) return;
        this.#connection = connectSSE({
            path: "/api/notifications/stream",
            eventTypes: ["notification.created", "notification.updated"],
            onOpen: () => {
                this.#connected = true;
                connectionStore.markConnected();
            },
            onError: (info) => {
                this.#connected = false;
                if (info.status !== 401) {
                    connectionStore.markDisconnected(info.message ?? "Notifications stream error");
                }
            },
            onEvent: (eventType, data) => {
                try {
                    const raw: unknown = JSON.parse(data);
                    const parsed = streamEnvelopeSchema.safeParse(raw);
                    if (!parsed.success) {
                        this.#logger.warn("Invalid notification SSE payload", parsed.error.message);
                        return;
                    }
                    this.#applyUpdate(eventType, parsed.data.notification);
                } catch (e) {
                    this.#logger.error("Malformed notification SSE event", e);
                }
            },
            deps: {
                createEventSource: this.#createEventSource,
                getApiUrl: this.#getApiUrl,
            },
        });
    }

    #applyUpdate(eventType: string, n: Notification): void {
        const existing = this.#items.find((x) => x.id === n.id);
        const isNewer = this.#isNewerThanMarker(n);
        if (eventType === "notification.created" && !existing) {
            this.#items = [n, ...this.#items];
            if (isNewer) this.#unread += 1;
            return;
        }
        // Updated path: rewrite the item in place by id; if it wasn't tracked,
        // prepend it (server shouldn't normally publish "updated" for an
        // unknown id, but we handle it defensively).
        if (existing) {
            const next = this.#items.slice();
            const idx = next.findIndex((x) => x.id === n.id);
            if (idx >= 0) {
                next[idx] = n;
                this.#items = next;
            } else {
                this.#items = [n, ...next];
            }
            // Treat subsequent occurrences as a new unread bump only when count grew
            // and the latest occurrence postdates the user's mark-read marker.
            if (n.count > existing.count && isNewer) {
                this.#unread += n.count - existing.count;
            }
            return;
        }
        this.#items = [n, ...this.#items];
        if (isNewer) this.#unread += n.count;
    }

    #isNewerThanMarker(n: Notification): boolean {
        if (this.#lastReadAtMs <= 0) return true;
        const ts = Date.parse(n.last_occurred_at);
        if (Number.isNaN(ts)) return true;
        return ts > this.#lastReadAtMs;
    }

    #setLastReadAt(value: string | null | undefined): void {
        if (!value) {
            this.#lastReadAt = null;
            this.#lastReadAtMs = 0;
            return;
        }
        const ms = Date.parse(value);
        if (Number.isNaN(ms) || ms <= 0) {
            this.#lastReadAt = null;
            this.#lastReadAtMs = 0;
            return;
        }
        this.#lastReadAt = value;
        this.#lastReadAtMs = ms;
    }

    async #fetchPage(
        before?: string,
    ): Promise<{ items: Notification[]; next_cursor?: string | undefined }> {
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

    async #fetchUnread(): Promise<{ count: number; lastReadAt: string | undefined }> {
        const res = await this.#fetch(`${this.#getApiUrl()}/api/notifications/unread-count`, {
            credentials: "include",
            headers: authHeaders(),
        });
        if (!res.ok) throw new Error(`Unread returned ${res.status.toString()}`);
        const raw: unknown = await res.json();
        const parsed = unreadResponseSchema.parse(raw);
        return { count: parsed.count, lastReadAt: parsed.last_read_at };
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
