// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";
import { authApi } from "$lib/api";
import { createLogger } from "$lib/utils/logger";
import { browserTokenStorage } from "$lib/adapters/browser";
import type { AuthState } from "$lib/types";

const logger = createLogger("AuthStore");

function createAuthStore() {
    const tokenStorage = browserTokenStorage;
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
            const hasToken = !!tokenStorage.getToken();
            // Trust the server's authenticated field (cookie session) OR a local token.
            const isAuthenticated = data.authenticated || hasToken;
            state = {
                required: data.authRequired,
                loaded: true,
                authenticated: !data.authRequired || isAuthenticated,
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
