// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";
import { authApi } from "$lib/api";
import { createLogger } from "$lib/utils/logger";
import type { AuthState } from "$lib/types";

const logger = createLogger("AuthStore");

function createAuthStore() {
    let state = $state<AuthState>({
        required: true,
        loaded: false,
        authenticated: false,
    });

    async function load(): Promise<void> {
        if (!browser) return;
        try {
            const data = await authApi.status();
            logger.info("Auth status loaded", data);
            // The server is the sole authority: it reports whether the HttpOnly
            // session cookie is a valid session. There is no client-held token.
            state = {
                required: data.authRequired,
                loaded: true,
                authenticated: !data.authRequired || data.authenticated,
            };
        } catch (error) {
            logger.error("Failed to load auth status", error);
            state = {
                required: true,
                loaded: true,
                authenticated: false,
            };
        }
    }

    return {
        get current() {
            return state;
        },
        load,
        markAuthenticated() {
            state = { ...state, authenticated: true };
        },
        markUnauthenticated() {
            state = { ...state, authenticated: false };
        },
    };
}

export const authStore = createAuthStore();
