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
} as const;

export const HTTP_STATUS = {
    UNAUTHORIZED: 401,
} as const;
