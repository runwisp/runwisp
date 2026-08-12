// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { EventSourcePolyfill } from "event-source-polyfill";
import { AUTH_EVENTS } from "$lib/config/constants";

/**
 * Minimal SSE consumer surface — exactly what the EventManager and connectSSE
 * helper use. Both the native `EventSource` and `EventSourcePolyfill`
 * structurally satisfy this, and test fakes only need to provide these few
 * members. Callbacks omit a `this` type so values typed against EventSource
 * (which binds `this: EventSource`) remain assignable.
 */
export interface SSEStream {
    readonly readyState: number;
    onopen: ((ev: Event) => unknown) | null;
    onerror: ((ev: Event) => unknown) | null;
    onmessage: ((ev: MessageEvent) => unknown) | null;
    close(): void;
    addEventListener(
        type: string,
        listener: (event: MessageEvent) => void,
        options?: boolean | AddEventListenerOptions,
    ): void;
}

export type EventSourceFactory = (url: string) => SSEStream;

export const browserAuthEventBus = {
    onAuthRequired(handler: EventListener) {
        globalThis.addEventListener(AUTH_EVENTS.REQUIRED, handler);
        return () => {
            globalThis.removeEventListener(AUTH_EVENTS.REQUIRED, handler);
        };
    },
    emitAuthRequired() {
        globalThis.dispatchEvent(new CustomEvent(AUTH_EVENTS.REQUIRED));
    },
};

/**
 * Auth-aware SSE factory. The browser session is authenticated solely by the
 * HttpOnly session cookie, so it opens the stream with `withCredentials: true`
 * to send that cookie. The JWT is never held in JS-readable storage — that is
 * the whole point of the cookie being HttpOnly — so there is no Bearer path here.
 */
export const browserAuthEventSourceFactory: EventSourceFactory = (url) => {
    return new EventSourcePolyfill(url, { withCredentials: true });
};
