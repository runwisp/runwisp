// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { browser } from "$app/environment";
import { DEFAULT_API_URL } from "$lib/config/constants";

export function getApiUrl(): string {
    if (!browser) {
        return DEFAULT_API_URL;
    }

    const rawUrl: unknown = import.meta.env.VITE_API_URL;
    const url = typeof rawUrl === "string" ? rawUrl : DEFAULT_API_URL;

    if (url === "") {
        return "";
    }

    if (/^(https?:)?\/\//.test(url)) {
        try {
            new URL(url);
            return url;
        } catch {
            console.warn(`Invalid VITE_API_URL: "${url}", falling back to ${DEFAULT_API_URL}`);
            return DEFAULT_API_URL;
        }
    }

    return url;
}
