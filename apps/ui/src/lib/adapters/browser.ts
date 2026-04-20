// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { EventSourcePolyfill } from "event-source-polyfill";
import { AUTH_TOKEN_KEY, AUTH_EVENTS } from "$lib/config/constants";

export type EventSourceFactory = (url: string) => EventSource;

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
        window.addEventListener(AUTH_EVENTS.REQUIRED, handler);
        return () => {
            window.removeEventListener(AUTH_EVENTS.REQUIRED, handler);
        };
    },
    onAuthSuccess(handler: EventListener) {
        window.addEventListener(AUTH_EVENTS.SUCCESS, handler);
        return () => {
            window.removeEventListener(AUTH_EVENTS.SUCCESS, handler);
        };
    },
    emitAuthRequired() {
        window.dispatchEvent(new CustomEvent(AUTH_EVENTS.REQUIRED));
    },
    emitAuthSuccess() {
        window.dispatchEvent(new CustomEvent(AUTH_EVENTS.SUCCESS));
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
    // Cookie-based auth: let the browser send the HttpOnly cookie.
    return new EventSourcePolyfill(url, { withCredentials: true });
};
