// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { EventSourceFactory } from "$lib/adapters/browser";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { type SSEErrorInfo, getMessageEventData } from "$lib/utils/event-source";
import { createLogger } from "$lib/utils/logger";
import { createReconnectingConnection } from "$lib/utils/sse-reconnect";

interface SSEOptions {
    /** URL path, resolved on every (re)connect so it can carry a resume offset. */
    path: () => string;
    /** Named SSE event types to dispatch to onEvent. */
    eventTypes: string[];
    onEvent: (eventType: string, data: string) => void;
    onOpen?: () => void;
    /** Called on connection error (before reconnect). */
    onError?: (info: SSEErrorInfo) => void;
    /** Defaults to the auth-aware browser factory; tests pass a fake. */
    createEventSource?: EventSourceFactory;
}

/**
 * Opens an auth-aware SSE connection that reconnects with exponential backoff
 * and dispatches each named event to `onEvent`.
 */
export function connectSSE(options: SSEOptions): { disconnect(): void } {
    const { path, eventTypes, onEvent, onOpen, onError } = options;

    const connection = createReconnectingConnection({
        resolve: () => {
            const resolvedPath = path();
            return { url: resolvedPath, label: resolvedPath };
        },
        createEventSource: options.createEventSource ?? browserAuthEventSourceFactory,
        logger: createLogger("SSE"),
        onCreated: (es) => {
            for (const eventType of eventTypes) {
                es.addEventListener(eventType, (event: MessageEvent) => {
                    const data = getMessageEventData(event);
                    if (data !== undefined) onEvent(eventType, data);
                });
            }
        },
        onOpen: () => onOpen?.(),
        onError: (info) => onError?.(info),
        shouldReconnect: () => true,
    });

    connection.connect();

    return { disconnect: connection.dispose };
}
