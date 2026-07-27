// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { HandleClientError } from "@sveltejs/kit";
import { connectionStore } from "$lib/stores/connection.svelte";

const CONNECTION_ERROR = /failed to fetch|dynamically imported module|load.*chunk|networkerror/i;

export const handleError: HandleClientError = ({ error, message }) => {
    // A failed dynamic import on navigation means the file server is down.
    // Flag the connection store so the polished offline UX + auto-retry take over.
    const text = error instanceof Error ? `${message} ${error.message}` : message;
    if (CONNECTION_ERROR.test(text)) {
        connectionStore.markDisconnected(error);
    }
    return { message };
};
