// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export const DEFAULT_API_URL = "";

export const AUTH_TOKEN_KEY = "runwisp_token";

export const AUTH_EVENTS = {
    REQUIRED: "auth-required",
    SUCCESS: "auth-success",
} as const;

export const SSE_CONFIG = {
    RECONNECT_DELAY: 3000,
    MAX_RECONNECT_DELAY: 30000,
    // If an EventSource is created but fires neither `open` nor `error` within
    // this window, treat it as stalled. The dominant cause is the browser's
    // per-origin connection cap (~6 over HTTP/1.1, shared across all tabs):
    // a queued SSE request stays in CONNECTING indefinitely with no event, so
    // only a wall-clock timeout can surface it. Generous because a healthy
    // local daemon opens in well under a second.
    OPEN_TIMEOUT: 8000,
} as const;

export const HTTP_STATUS = {
    UNAUTHORIZED: 401,
    TOO_MANY_REQUESTS: 429,
} as const;
