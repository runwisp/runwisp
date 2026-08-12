// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { SSE_CONFIG } from "$lib/config/constants";
import {
    browserAuthEventSourceFactory,
    type EventSourceFactory,
    type SSEStream,
} from "$lib/adapters/browser";
import { type SSEErrorInfo, getMessageEventData } from "$lib/utils/event-source";
import { getApiUrl as defaultGetApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";
import {
    createReconnectingConnection,
    type ReconnectingConnection,
} from "$lib/utils/sse-reconnect";

export type EventManagerErrorInfo = SSEErrorInfo;

// The optional `id` is the SSE event's Last-Event-ID (the server's monotonic
// sequence). SharedAppStream tracks it to seed a freshly-opened EventSource's
// resume cursor; most consumers ignore it.
export type EventHandler = (data: string, id?: string) => void;
export type OpenHandler = () => void;
export type ErrorHandler = (info: EventManagerErrorInfo) => void;
export type StallHandler = () => void;

/**
 * The surface every app-event-stream source exposes to its consumers, whether
 * the source is a direct {@link EventManager} (one EventSource for this tab) or
 * the cross-tab {@link import("./shared-app-stream").SharedAppStream} (one
 * EventSource shared by all tabs, the rest riding a BroadcastChannel). Stores
 * code against this interface so they don't care which transport they got.
 */
export interface AppEventStream {
    subscribe(eventType: string, handler: EventHandler): () => void;
    onOpen(handler: OpenHandler): () => void;
    onError(handler: ErrorHandler): () => void;
    /**
     * Fires when the connection was created but has neither opened nor errored
     * within {@link SSE_CONFIG.OPEN_TIMEOUT} — i.e. it is stuck CONNECTING,
     * almost always because the browser's per-origin connection cap is full.
     */
    onStall(handler: StallHandler): () => void;
}

export interface EventManagerOptions {
    /** SSE path relative to the API root, e.g. `/api/runs/stream`. */
    path: string;
    /**
     * Factory for the underlying EventSource. Defaults to the auth-aware factory
     * (Bearer header when JWT is present in localStorage; cookie auth otherwise).
     */
    createEventSource?: EventSourceFactory;
    /** Resolves the API base URL at connection time. */
    getApiUrl?: () => string;
    /**
     * Seeds the resume cursor for a freshly-opened connection, appended as
     * `?lastEventId=`. A same-EventSource reconnect resends `Last-Event-ID`
     * natively, but a brand-new EventSource (e.g. a promoted cross-tab leader)
     * starts with an empty one — this lets it resume from the id the cohort last
     * saw so the server replays the handoff gap. Returns null for a fresh start.
     */
    initialLastEventId?: () => string | null;
}

/**
 * EventManager owns a single EventSource per path. Reconnect with exponential
 * backoff lives here, not in callers. Multiple subscribers per event type are
 * fanned out; the connection opens lazily on first subscription and closes
 * automatically when the last handler unsubscribes.
 */
export class EventManager implements AppEventStream {
    readonly #path: string;
    readonly #logger = createLogger("EventManager");
    readonly #connection: ReconnectingConnection;

    // Plain (non-reactive) collections: this is internal connection plumbing,
    // never a reactive UI source. Using Svelte reactive collections here made
    // subscribe()/unsubscribe() read+write a tracked source, so calling
    // subscribe() inside an $effect self-invalidated the effect into an
    // infinite subscribe/teardown loop that tore the EventSource down on every
    // tick. Nothing reactively reads who is subscribed — keep these plain.
    readonly #handlers = new Map<string, Set<EventHandler>>();
    readonly #openHandlers = new Set<OpenHandler>();
    readonly #errorHandlers = new Set<ErrorHandler>();
    readonly #stallHandlers = new Set<StallHandler>();

    #openTimer: ReturnType<typeof setTimeout> | null = null;
    #closed = false;

    constructor(options: EventManagerOptions) {
        this.#path = options.path;
        const getApiUrl = options.getApiUrl ?? defaultGetApiUrl;
        const createEventSource = options.createEventSource ?? browserAuthEventSourceFactory;
        const initialLastEventId = options.initialLastEventId;

        this.#connection = createReconnectingConnection({
            resolve: () => {
                const base = `${getApiUrl()}${this.#path}`;
                const id = initialLastEventId?.();
                const url = id ? `${base}?lastEventId=${encodeURIComponent(id)}` : base;
                return { url, label: this.#path };
            },
            createEventSource,
            logger: this.#logger,
            onCreated: (es) => {
                this.#startOpenTimer();
                for (const eventType of this.#handlers.keys()) {
                    this.#bindEventType(es, eventType);
                }
            },
            onOpen: () => {
                this.#clearOpenTimer();
                for (const handler of this.#openHandlers) {
                    try {
                        handler();
                    } catch (err) {
                        this.#logger.warn("onOpen handler threw", err);
                    }
                }
            },
            onError: (info) => {
                this.#clearOpenTimer();
                this.#notifyError(info);
            },
            shouldReconnect: () => this.#totalSubscribers() > 0,
        });
    }

    /** Subscribe to a named SSE event type. Returns an unsubscribe function. */
    subscribe(eventType: string, handler: EventHandler): () => void {
        let set = this.#handlers.get(eventType);
        if (!set) {
            set = new Set();
            this.#handlers.set(eventType, set);
            // Bind the listener if the connection is already open; otherwise it
            // will be bound when connect() runs.
            const stream = this.#connection.getStream();
            if (stream) {
                this.#bindEventType(stream, eventType);
            }
        }
        set.add(handler);

        this.#ensureConnected();

        return () => {
            this.unsubscribe(eventType, handler);
        };
    }

    /** Remove a previously registered handler. */
    unsubscribe(eventType: string, handler: EventHandler): void {
        const set = this.#handlers.get(eventType);
        if (!set) return;
        set.delete(handler);
        if (set.size === 0) {
            this.#handlers.delete(eventType);
        }
        if (this.#totalSubscribers() === 0) {
            this.#clearOpenTimer();
            this.#connection.stop();
        }
    }

    /** Subscribe to lifecycle "open" callbacks (fires on each successful (re)connect). */
    onOpen(handler: OpenHandler): () => void {
        this.#openHandlers.add(handler);
        return () => {
            this.#openHandlers.delete(handler);
        };
    }

    /** Subscribe to error notifications (fires before each reconnect attempt). */
    onError(handler: ErrorHandler): () => void {
        this.#errorHandlers.add(handler);
        return () => {
            this.#errorHandlers.delete(handler);
        };
    }

    /** Subscribe to stall notifications (fires when a connect attempt hangs open). */
    onStall(handler: StallHandler): () => void {
        this.#stallHandlers.add(handler);
        return () => {
            this.#stallHandlers.delete(handler);
        };
    }

    /** Tear down the connection and clear all subscribers. */
    close(): void {
        this.#closed = true;
        this.#handlers.clear();
        this.#openHandlers.clear();
        this.#errorHandlers.clear();
        this.#stallHandlers.clear();
        this.#clearOpenTimer();
        this.#connection.dispose();
    }

    #totalSubscribers(): number {
        let count = 0;
        for (const set of this.#handlers.values()) {
            count += set.size;
        }
        return count;
    }

    #ensureConnected(): void {
        if (this.#connection.getStream() || this.#closed) return;
        this.#connection.connect();
    }

    // A connect attempt that fires neither `open` nor `error` within the window
    // is stalled — the browser is holding the request queued behind other
    // long-lived connections to this origin. We keep the EventSource pending
    // (the browser opens it once a slot frees, firing `open` → recovery) and
    // just surface the stall so the UI can explain it. No teardown, no
    // reconnect churn: re-creating the request would only re-queue it.
    #startOpenTimer(): void {
        this.#clearOpenTimer();
        this.#openTimer = setTimeout(() => {
            this.#openTimer = null;
            this.#notifyStall();
        }, SSE_CONFIG.OPEN_TIMEOUT);
    }

    #clearOpenTimer(): void {
        if (this.#openTimer) {
            clearTimeout(this.#openTimer);
            this.#openTimer = null;
        }
    }

    #bindEventType(es: SSEStream, eventType: string): void {
        es.addEventListener(eventType, (event: MessageEvent) => {
            const data = getMessageEventData(event);
            if (data === undefined) return;
            const id = event.lastEventId || undefined;
            const set = this.#handlers.get(eventType);
            if (!set) return;
            for (const handler of set) {
                try {
                    handler(data, id);
                } catch (err) {
                    this.#logger.error(`handler for ${eventType} threw`, err);
                }
            }
        });
    }

    #notifyError(info: EventManagerErrorInfo): void {
        for (const handler of this.#errorHandlers) {
            try {
                handler(info);
            } catch (err) {
                this.#logger.warn("onError handler threw", err);
            }
        }
    }

    #notifyStall(): void {
        this.#logger.warn(`SSE connect to ${this.#path} stalled (no open within timeout)`);
        for (const handler of this.#stallHandlers) {
            try {
                handler();
            } catch (err) {
                this.#logger.warn("onStall handler threw", err);
            }
        }
    }
}
