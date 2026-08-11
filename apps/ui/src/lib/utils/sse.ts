// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { EventSourceFactory } from "$lib/adapters/browser";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { type SSEErrorInfo, getMessageEventData } from "$lib/utils/event-source";
import { getApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";
import { createReconnectingConnection } from "$lib/utils/sse-reconnect";

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

    const connection = createReconnectingConnection({
        resolve: () => {
            const resolvedPath = typeof path === "function" ? path() : path;
            return { url: buildSSEUrl(resolvedPath, resolveApiUrl()), label: resolvedPath };
        },
        createEventSource,
        logger,
        onCreated: (es) => {
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
        },
        onOpen: () => onOpen?.(),
        onError: (info) => onError?.(info),
        shouldReconnect: () => reconnect,
    });

    connection.connect();

    return {
        disconnect: () => {
            connection.dispose();
        },
    };
}
