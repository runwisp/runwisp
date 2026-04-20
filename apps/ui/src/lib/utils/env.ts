// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { browser } from "$app/environment";
import { DEFAULT_API_URL } from "$lib/config/constants";

export function getApiUrl(): string {
    if (!browser) {
        return DEFAULT_API_URL;
    }

    const rawUrl: unknown = import.meta.env.VITE_API_URL;
    const url = typeof rawUrl === "string" ? rawUrl : DEFAULT_API_URL;

    // If empty string - use relative URL (same origin)
    if (url === "") {
        return "";
    }

    // If URL looks absolute (starts with http://, https:// or //), validate it
    if (/^(https?:)?\/\//.test(url)) {
        try {
            new URL(url);
            return url;
        } catch {
            console.warn(`Invalid VITE_API_URL: "${url}", falling back to ${DEFAULT_API_URL}`);
            return DEFAULT_API_URL;
        }
    }

    // Otherwise assume it's a relative path like "/api" or "api"
    return url;
}
