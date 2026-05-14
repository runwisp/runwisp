// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SvelteMap, SvelteSet } from "svelte/reactivity";
import { SSE_CONFIG } from "$lib/config/constants";
import {
    browserAuthEventSourceFactory,
    type EventSourceFactory,
    type SSEStream,
} from "$lib/adapters/browser";
import { getEventSourceErrorDetails, getMessageEventData } from "$lib/utils/event-source";
import { getApiUrl as defaultGetApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

export interface EventManagerErrorInfo {
    status?: number;
    message?: string;
    readyState?: number;
    url?: string;
}

export type EventHandler = (data: string) => void;
export type OpenHandler = () => void;
export type ErrorHandler = (info: EventManagerErrorInfo) => void;

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
export class EventManager {
    readonly #path: string;
    readonly #createEventSource: EventSourceFactory;
    readonly #getApiUrl: () => string;
    readonly #logger = createLogger("EventManager");

    readonly #handlers = new SvelteMap<string, SvelteSet<EventHandler>>();
    readonly #openHandlers = new SvelteSet<OpenHandler>();
    readonly #errorHandlers = new SvelteSet<ErrorHandler>();

    #eventSource: SSEStream | null = null;
    #reconnectTimer: ReturnType<typeof setTimeout> | null = null;
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
            set = new SvelteSet();
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

    /** Tear down the connection and clear all subscribers. */
    close(): void {
        this.#closed = true;
        this.#handlers.clear();
        this.#openHandlers.clear();
        this.#errorHandlers.clear();
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
        es.onopen = () => {
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
            const info = this.#extractErrorInfo(event, es, url);
            this.#logger.warn(`SSE error on ${this.#path}: ${this.#formatErrorInfo(info)}`);
            this.#notifyError(info);
            this.#cleanup();
            this.#scheduleReconnect();
        };

        for (const eventType of this.#handlers.keys()) {
            this.#bindEventType(es, eventType);
        }
    }

    #bindEventType(es: SSEStream, eventType: string): void {
        es.addEventListener(eventType, (event: MessageEvent) => {
            const data = getMessageEventData(event);
            if (typeof data === "undefined") return;
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

    #extractErrorInfo(event: Event, es: SSEStream, url: string): EventManagerErrorInfo {
        const { status, message } = getEventSourceErrorDetails(event);
        return {
            ...(typeof status !== "undefined" && { status }),
            ...(typeof message !== "undefined" && { message }),
            readyState: es.readyState,
            url,
        };
    }

    #formatErrorInfo(info: EventManagerErrorInfo): string {
        const parts: string[] = [];
        if (typeof info.status !== "undefined") parts.push(`status=${info.status.toString()}`);
        if (typeof info.message !== "undefined") parts.push(info.message);
        if (typeof info.readyState !== "undefined")
            parts.push(`readyState=${info.readyState.toString()}`);
        if (typeof info.url !== "undefined") parts.push(info.url);
        return parts.join(" ") || "unknown error";
    }
}
