// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { EventSourcePolyfill } from "event-source-polyfill";
import { AUTH_TOKEN_KEY, AUTH_EVENTS } from "$lib/config/constants";

/**
 * Minimal SSE consumer surface — exactly what the EventManager and legacy
 * connectSSE helper use. Both the native `EventSource` and `EventSourcePolyfill`
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

export const browserTokenStorage = {
    getToken: () => localStorage.getItem(AUTH_TOKEN_KEY),
    setToken: (token: string) => {
        localStorage.setItem(AUTH_TOKEN_KEY, token);
    },
    removeToken: () => {
        localStorage.removeItem(AUTH_TOKEN_KEY);
    },
};

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

export const browserEventSourceFactory: EventSourceFactory = (url) => new EventSourcePolyfill(url);

/**
 * Auth-aware SSE factory. When a JWT exists in localStorage, sends it as an
 * `Authorization: Bearer` header via the fetch-based polyfill (the native
 * EventSource API cannot send custom headers). When no localStorage token is
 * present (cookie-based auth from launch ticket), falls back to
 * `withCredentials: true` so the browser sends the HttpOnly cookie.
 */
export const browserAuthEventSourceFactory: EventSourceFactory = (url) => {
    const token = browserTokenStorage.getToken();
    if (token) {
        return new EventSourcePolyfill(url, {
            headers: { Authorization: `Bearer ${token}` },
        });
    }
    return new EventSourcePolyfill(url, { withCredentials: true });
};
