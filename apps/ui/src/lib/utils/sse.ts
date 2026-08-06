// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { SSE_CONFIG } from "$lib/config/constants";
import type { EventSourceFactory, SSEStream } from "$lib/adapters/browser";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import {
    type SSEErrorInfo,
    extractErrorInfo,
    formatErrorInfo,
    getMessageEventData,
} from "$lib/utils/event-source";
import { getApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

export interface SSEConnectionDeps {
    createEventSource?: EventSourceFactory;
    getApiUrl?: () => string;
}

export function buildSSEUrl(path: string, apiUrl: string = getApiUrl()): string {
    return `${apiUrl}${path}`;
}

export interface ReconnectingSSEOptions {
    /**
     * URL path to connect to (will be prefixed with apiUrl). May be a function
     * to allow per-reconnect resumption parameters (e.g. a byte offset).
     */
    path: string | (() => string);
    /** Called for each named SSE event */
    onEvent: (eventType: string, data: string) => void;
    /** Called when connection is established */
    onOpen?: () => void;
    /** Called on connection error (before reconnect) */
    onError?: (info: SSEErrorInfo) => void;
    /**
     * Event type names to listen for. If empty, uses `onmessage`. To receive
     * both the default unnamed events and named events, include `"message"`.
     */
    eventTypes?: string[];
    /** Whether to auto-reconnect on error. Default: true */
    reconnect?: boolean;
    deps?: SSEConnectionDeps;
}

export interface SSEConnection {
    disconnect(): void;
}

/**
 * Creates an SSE connection with automatic token auth, exponential backoff
 * reconnection, and typed event dispatching.
 */
export function connectSSE(options: ReconnectingSSEOptions): SSEConnection {
    const {
        path,
        onEvent,
        onOpen,
        onError,
        eventTypes = [],
        reconnect = true,
        deps = {},
    } = options;

    const createEventSource = deps.createEventSource ?? browserAuthEventSourceFactory;
    const resolveApiUrl = deps.getApiUrl ?? getApiUrl;
    const logger = createLogger("SSE");

    let eventSource: SSEStream | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay: number = SSE_CONFIG.RECONNECT_DELAY;
    let disposed = false;

    function connect() {
        if (disposed) return;

        const resolvedPath = typeof path === "function" ? path() : path;
        const url = buildSSEUrl(resolvedPath, resolveApiUrl());
        let es: SSEStream;
        try {
            es = createEventSource(url);
        } catch (err) {
            logger.warn("SSE failed to create EventSource for " + resolvedPath, err);
            onError?.({ message: String(err), url });
            scheduleReconnect();
            return;
        }
        eventSource = es;

        es.onopen = () => {
            reconnectDelay = SSE_CONFIG.RECONNECT_DELAY;
            onOpen?.();
        };

        es.onerror = (e: Event) => {
            const info = extractErrorInfo(e, es, url);
            logger.warn(`SSE error on ${resolvedPath}: ${formatErrorInfo(info)}`);
            onError?.(info);
            cleanup();
            scheduleReconnect();
        };

        if (eventTypes.length > 0) {
            for (const eventType of eventTypes) {
                es.addEventListener(eventType, (event: MessageEvent) => {
                    const data = getMessageEventData(event);
                    if (data !== undefined) {
                        onEvent(eventType, data);
                    }
                });
            }
        } else {
            es.onmessage = (event: MessageEvent) => {
                const data = getMessageEventData(event);
                if (data !== undefined) {
                    onEvent("message", data);
                }
            };
        }
    }

    function scheduleReconnect() {
        if (reconnect && !disposed) {
            const delay = reconnectDelay;
            reconnectDelay = Math.min(reconnectDelay * 2, SSE_CONFIG.MAX_RECONNECT_DELAY);
            reconnectTimeout = setTimeout(connect, delay);
        }
    }

    function cleanup() {
        if (eventSource) {
            eventSource.close();
            eventSource = null;
        }
    }

    function disconnect() {
        disposed = true;
        if (reconnectTimeout) {
            clearTimeout(reconnectTimeout);
            reconnectTimeout = null;
        }
        cleanup();
    }

    connect();

    return { disconnect };
}
