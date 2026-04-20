// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { SSE_CONFIG } from "$lib/config/constants";
import type { EventSourceFactory } from "$lib/adapters/browser";
import { browserEventSourceFactory } from "$lib/adapters/browser";
import { getEventSourceErrorDetails, getMessageEventData } from "$lib/utils/event-source";
import { getApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

export interface SSEConnectionDeps {
    createEventSource?: EventSourceFactory;
    getApiUrl?: () => string;
}

export function buildSSEUrl(path: string, apiUrl: string = getApiUrl()): string {
    return `${apiUrl}${path}`;
}

export interface SSEErrorInfo {
    status?: number;
    message?: string;
    readyState?: number;
    url?: string;
}

export interface ReconnectingSSEOptions {
    /** URL path (will be prefixed with apiUrl and get token appended) */
    path: string;
    /** Called for each named SSE event */
    onEvent: (eventType: string, data: string) => void;
    /** Called when connection is established */
    onOpen?: () => void;
    /** Called on connection error (before reconnect) */
    onError?: (info: SSEErrorInfo) => void;
    /** Event type names to listen for. If empty, uses `onmessage`. */
    eventTypes?: string[];
    /** Whether to auto-reconnect on error. Default: true */
    reconnect?: boolean;
    deps?: SSEConnectionDeps;
}

export interface SSEConnection {
    disconnect(): void;
}

/** Extract useful debugging info from an EventSource error event. */
function extractErrorInfo(e: Event, es: EventSource, url: string): SSEErrorInfo {
    const { status, message } = getEventSourceErrorDetails(e);
    return {
        ...(status !== undefined && { status }),
        ...(message !== undefined && { message }),
        readyState: es.readyState,
        url,
    };
}

function formatErrorInfo(info: SSEErrorInfo): string {
    const parts: string[] = [];
    if (info.status !== undefined) parts.push(`status=${info.status.toString()}`);
    if (info.message !== undefined) parts.push(info.message);
    if (info.readyState !== undefined) parts.push(`readyState=${info.readyState.toString()}`);
    if (info.url !== undefined) parts.push(info.url);
    return parts.join(" ") || "unknown error";
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

    const createEventSource = deps.createEventSource ?? browserEventSourceFactory;
    const resolveApiUrl = deps.getApiUrl ?? getApiUrl;
    const logger = createLogger("SSE");

    let eventSource: EventSource | null = null;
    let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay: number = SSE_CONFIG.RECONNECT_DELAY;
    let disposed = false;

    function connect() {
        if (disposed) return;

        const url = buildSSEUrl(path, resolveApiUrl());
        let es: EventSource;
        try {
            es = createEventSource(url);
        } catch (err) {
            logger.warn("SSE failed to create EventSource for " + path, err);
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
            logger.warn(`SSE error on ${path}: ${formatErrorInfo(info)}`);
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
