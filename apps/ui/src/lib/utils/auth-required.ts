// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";
import { browserAuthEventBus } from "$lib/adapters/browser";
import { HTTP_STATUS } from "$lib/config/constants";
import { authStore } from "$lib/stores/auth.svelte";

/**
 * The one reaction to an expired or rejected session: flip the auth store (the
 * root layout then tears the live stores down) and ask the login modal to open.
 * Every 401 source (REST middleware, raw fetches, SSE errors) routes here.
 */
export function handleUnauthorized(): void {
    if (!browser) return;
    authStore.markUnauthenticated();
    browserAuthEventBus.emitAuthRequired();
}

/** `fetch` that routes a 401 response through {@link handleUnauthorized}. */
export async function authFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
    const response = await fetch(input, init);
    if (response.status === HTTP_STATUS.UNAUTHORIZED) handleUnauthorized();
    return response;
}
