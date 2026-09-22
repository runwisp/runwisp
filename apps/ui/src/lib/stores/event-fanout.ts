// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Logger } from "@runwisp/common";
import type { SSEErrorInfo } from "$lib/utils/event-source";

// The optional `id` is the SSE event's Last-Event-ID (the server's monotonic
// sequence). SharedAppStream tracks it to seed a freshly-opened EventSource's
// resume cursor; most consumers ignore it.
export type EventHandler = (data: string, id?: string) => void;
export type OpenHandler = () => void;
export type StallHandler = () => void;
export type ErrorHandler = (info: SSEErrorInfo) => void;

/**
 * One named "signal" with any number of listeners — the shape EventManager
 * and SharedAppStream each repeat three times over (open/error/stall):
 * subscribing adds a listener and returns an unsubscribe function, emitting
 * invokes every listener and logs (rather than throws) if one does.
 */
export class Signal<Args extends unknown[]> {
    readonly #listeners = new Set<(...args: Args) => void>();

    constructor(
        private readonly logger: Logger,
        private readonly label: string,
    ) {}

    add(listener: (...args: Args) => void): () => void {
        this.#listeners.add(listener);
        return () => {
            this.#listeners.delete(listener);
        };
    }

    emit(...args: Args): void {
        for (const listener of this.#listeners) {
            try {
                listener(...args);
            } catch (err) {
                this.logger.warn(`${this.label} handler threw`, err);
            }
        }
    }

    clear(): void {
        this.#listeners.clear();
    }
}

/**
 * Per-event-type fan-out: many handlers per SSE event type, dispatched with
 * the same try/catch-and-log shape. EventManager and SharedAppStream each
 * keep one of these for their subscribers; plain (non-reactive) storage —
 * this is connection plumbing, never a reactive UI source (see the note on
 * EventManager's own field for why that distinction matters here).
 */
export class HandlerRegistry {
    readonly #handlers = new Map<string, Set<EventHandler>>();

    /** Adds `handler` under `eventType`; returns true if it's the first for that type. */
    add(eventType: string, handler: EventHandler): boolean {
        let set = this.#handlers.get(eventType);
        const isFirst = !set;
        if (!set) {
            set = new Set();
            this.#handlers.set(eventType, set);
        }
        set.add(handler);
        return isFirst;
    }

    remove(eventType: string, handler: EventHandler): void {
        const set = this.#handlers.get(eventType);
        if (!set) return;
        set.delete(handler);
        if (set.size === 0) this.#handlers.delete(eventType);
    }

    keys(): IterableIterator<string> {
        return this.#handlers.keys();
    }

    totalSize(): number {
        let count = 0;
        for (const set of this.#handlers.values()) count += set.size;
        return count;
    }

    dispatch(eventType: string, data: string, id: string | undefined, logger: Logger): void {
        const set = this.#handlers.get(eventType);
        if (!set) return;
        for (const handler of set) {
            try {
                handler(data, id);
            } catch (err) {
                logger.error(`handler for ${eventType} threw`, err);
            }
        }
    }

    clear(): void {
        this.#handlers.clear();
    }
}
