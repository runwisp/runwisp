// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Logger } from "@runwisp/common";
import { SSE_CONFIG } from "$lib/config/constants";
import type { SSEStream } from "$lib/adapters/browser";
import { type SSEErrorInfo, extractErrorInfo, formatErrorInfo } from "$lib/utils/event-source";

export interface ReconnectingConnectionOptions {
    /**
     * Resolves the URL to connect to and the label used in log lines (e.g.
     * the bare path). Called once per (re)connect attempt — both values are
     * captured for the lifetime of that attempt, so a stateful URL (e.g. a
     * resumption offset) doesn't shift mid-attempt.
     */
    resolve: () => { url: string; label: string };
    /** Creates the underlying stream for a URL. May throw. */
    createEventSource: (url: string) => SSEStream;
    logger: Logger;
    /** Called right after a stream is created, before open/error can fire — bind listeners here. */
    onCreated: (es: SSEStream) => void;
    /** Called when the stream opens. Reconnect backoff has already been reset. */
    onOpen: () => void;
    /** Called on connection error, after logging and before teardown + reconnect scheduling. */
    onError: (info: SSEErrorInfo) => void;
    /** Checked before every reconnect attempt; return false to stop retrying. */
    shouldReconnect: () => boolean;
}

export interface ReconnectingConnection {
    /** (Re)connect now. No-op once disposed. */
    connect: () => void;
    /** The currently active stream, or null while disconnected/reconnecting. */
    getStream: () => SSEStream | null;
    /** Tear down the current attempt and cancel any pending reconnect timer. connect() may be called again afterward. */
    stop: () => void;
    /** Permanently stop; connect() becomes a no-op afterward. */
    dispose: () => void;
}

/**
 * Owns the exponential-backoff reconnect loop shared by every SSE consumer:
 * create the stream, wire open/error, extract + log errors, clean up, and
 * retry with backoff. Callers own event-type binding and anything on top of
 * that (multi-subscriber fan-out, stall detection, single-callback dispatch).
 */
export function createReconnectingConnection(
    options: ReconnectingConnectionOptions,
): ReconnectingConnection {
    let stream: SSEStream | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let reconnectDelay: number = SSE_CONFIG.RECONNECT_DELAY;
    let disposed = false;

    function connect(): void {
        if (disposed) return;

        const { url, label } = options.resolve();
        let es: SSEStream;
        try {
            es = options.createEventSource(url);
        } catch (err) {
            options.logger.warn(`SSE failed to create EventSource for ${label}`, err);
            options.onError({ message: String(err), url });
            scheduleReconnect();
            return;
        }
        stream = es;

        es.onopen = () => {
            reconnectDelay = SSE_CONFIG.RECONNECT_DELAY;
            options.onOpen();
        };
        es.onerror = (e: Event) => {
            const info = extractErrorInfo(e, es, url);
            options.logger.warn(`SSE error on ${label}: ${formatErrorInfo(info)}`);
            options.onError(info);
            cleanup();
            scheduleReconnect();
        };

        options.onCreated(es);
    }

    function scheduleReconnect(): void {
        if (disposed || !options.shouldReconnect()) return;
        const delay = reconnectDelay;
        reconnectDelay = Math.min(reconnectDelay * 2, SSE_CONFIG.MAX_RECONNECT_DELAY);
        reconnectTimer = setTimeout(() => {
            reconnectTimer = null;
            connect();
        }, delay);
    }

    function cleanup(): void {
        if (stream) {
            stream.close();
            stream = null;
        }
    }

    function stop(): void {
        if (reconnectTimer) {
            clearTimeout(reconnectTimer);
            reconnectTimer = null;
        }
        cleanup();
    }

    function dispose(): void {
        disposed = true;
        stop();
    }

    return {
        connect,
        getStream: () => stream,
        stop,
        dispose,
    };
}
