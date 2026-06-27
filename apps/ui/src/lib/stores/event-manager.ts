// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SSE_CONFIG } from "$lib/config/constants";
import {
    browserAuthEventSourceFactory,
    type EventSourceFactory,
    type SSEStream,
} from "$lib/adapters/browser";
import {
    type SSEErrorInfo,
    extractErrorInfo,
    formatErrorInfo,
    getMessageEventData,
} from "$lib/utils/event-source";
import { getApiUrl as defaultGetApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

export type EventManagerErrorInfo = SSEErrorInfo;

export type EventHandler = (data: string) => void;
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
}

/**
 * EventManager owns a single EventSource per path. Reconnect with exponential
 * backoff lives here, not in callers. Multiple subscribers per event type are
 * fanned out; the connection opens lazily on first subscription and closes
 * automatically when the last handler unsubscribes.
 */
export class EventManager implements AppEventStream {
    readonly #path: string;
    readonly #createEventSource: EventSourceFactory;
    readonly #getApiUrl: () => string;
    readonly #logger = createLogger("EventManager");

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

    #eventSource: SSEStream | null = null;
    #reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    #openTimer: ReturnType<typeof setTimeout> | null = null;
    #reconnectDelay: number = SSE_CONFIG.RECONNECT_DELAY;
    #closed = false;

    constructor(options: EventManagerOptions) {
        this.#path = options.path;
        this.#createEventSource = options.createEventSource ?? browserAuthEventSourceFactory;
        this.#getApiUrl = options.getApiUrl ?? defaultGetApiUrl;
    }

    /** Subscribe to a named SSE event type. Returns an unsubscribe function. */
    subscribe(eventType: string, handler: EventHandler): () => void {
        let set = this.#handlers.get(eventType);
        if (!set) {
            set = new Set();
            this.#handlers.set(eventType, set);
            // Bind the listener if the connection is already open; otherwise it
            // will be bound when connect() runs.
            if (this.#eventSource) {
                this.#bindEventType(this.#eventSource, eventType);
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
            this.#disconnect();
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
        this.#disconnect();
    }

    #totalSubscribers(): number {
        let count = 0;
        for (const set of this.#handlers.values()) {
            count += set.size;
        }
        return count;
    }

    #ensureConnected(): void {
        if (this.#eventSource || this.#closed) return;
        this.#connect();
    }

    #connect(): void {
        if (this.#closed) return;
        const url = `${this.#getApiUrl()}${this.#path}`;

        let es: SSEStream;
        try {
            es = this.#createEventSource(url);
        } catch (err) {
            this.#logger.warn(`failed to create EventSource for ${this.#path}`, err);
            this.#notifyError({ message: String(err), url });
            this.#scheduleReconnect();
            return;
        }

        this.#eventSource = es;
        this.#startOpenTimer();
        es.onopen = () => {
            this.#clearOpenTimer();
            this.#reconnectDelay = SSE_CONFIG.RECONNECT_DELAY;
            for (const handler of this.#openHandlers) {
                try {
                    handler();
                } catch (err) {
                    this.#logger.warn("onOpen handler threw", err);
                }
            }
        };
        es.onerror = (event: Event) => {
            this.#clearOpenTimer();
            const info = extractErrorInfo(event, es, url);
            this.#logger.warn(`SSE error on ${this.#path}: ${formatErrorInfo(info)}`);
            this.#notifyError(info);
            this.#cleanup();
            this.#scheduleReconnect();
        };

        for (const eventType of this.#handlers.keys()) {
            this.#bindEventType(es, eventType);
        }
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
            const set = this.#handlers.get(eventType);
            if (!set) return;
            for (const handler of set) {
                try {
                    handler(data);
                } catch (err) {
                    this.#logger.error(`handler for ${eventType} threw`, err);
                }
            }
        });
    }

    #scheduleReconnect(): void {
        if (this.#closed) return;
        if (this.#totalSubscribers() === 0) return;
        const delay = this.#reconnectDelay;
        this.#reconnectDelay = Math.min(this.#reconnectDelay * 2, SSE_CONFIG.MAX_RECONNECT_DELAY);
        this.#reconnectTimer = setTimeout(() => {
            this.#reconnectTimer = null;
            this.#connect();
        }, delay);
    }

    #disconnect(): void {
        if (this.#reconnectTimer) {
            clearTimeout(this.#reconnectTimer);
            this.#reconnectTimer = null;
        }
        this.#cleanup();
    }

    #cleanup(): void {
        this.#clearOpenTimer();
        if (this.#eventSource) {
            this.#eventSource.close();
            this.#eventSource = null;
        }
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
