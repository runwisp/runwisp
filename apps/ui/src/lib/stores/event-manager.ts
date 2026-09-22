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
import {
    HandlerRegistry,
    Signal,
    type EventHandler,
    type OpenHandler,
    type ErrorHandler,
    type StallHandler,
} from "./event-fanout";

export type EventManagerErrorInfo = SSEErrorInfo;

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
    /** SSE path relative to the API root, e.g. `/api/events/stream`. */
    path: string;
    /**
     * Factory for the underlying EventSource. Defaults to the auth-aware factory
     * (opens with withCredentials so the HttpOnly session cookie is sent).
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
    readonly #handlers = new HandlerRegistry();
    readonly #open = new Signal<[]>(this.#logger, "onOpen");
    readonly #error = new Signal<[EventManagerErrorInfo]>(this.#logger, "onError");
    readonly #stall = new Signal<[]>(this.#logger, "onStall");

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
                this.#open.emit();
            },
            onError: (info) => {
                this.#clearOpenTimer();
                this.#error.emit(info);
            },
            shouldReconnect: () => this.#handlers.totalSize() > 0,
        });
    }

    /** Subscribe to a named SSE event type. Returns an unsubscribe function. */
    subscribe(eventType: string, handler: EventHandler): () => void {
        const isFirst = this.#handlers.add(eventType, handler);
        if (isFirst) {
            // Bind the listener if the connection is already open; otherwise it
            // will be bound when connect() runs.
            const stream = this.#connection.getStream();
            if (stream) {
                this.#bindEventType(stream, eventType);
            }
        }

        this.#ensureConnected();

        return () => {
            this.unsubscribe(eventType, handler);
        };
    }

    /** Remove a previously registered handler. */
    unsubscribe(eventType: string, handler: EventHandler): void {
        this.#handlers.remove(eventType, handler);
        if (this.#handlers.totalSize() === 0) {
            this.#clearOpenTimer();
            this.#connection.stop();
        }
    }

    /** Subscribe to lifecycle "open" callbacks (fires on each successful (re)connect). */
    onOpen(handler: OpenHandler): () => void {
        return this.#open.add(handler);
    }

    /** Subscribe to error notifications (fires before each reconnect attempt). */
    onError(handler: ErrorHandler): () => void {
        return this.#error.add(handler);
    }

    /** Subscribe to stall notifications (fires when a connect attempt hangs open). */
    onStall(handler: StallHandler): () => void {
        return this.#stall.add(handler);
    }

    /** Tear down the connection and clear all subscribers. */
    close(): void {
        this.#closed = true;
        this.#handlers.clear();
        this.#open.clear();
        this.#error.clear();
        this.#stall.clear();
        this.#clearOpenTimer();
        this.#connection.dispose();
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
            this.#logger.warn(`SSE connect to ${this.#path} stalled (no open within timeout)`);
            this.#stall.emit();
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
            this.#handlers.dispatch(eventType, data, id, this.#logger);
        });
    }
}
