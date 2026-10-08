// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { SSEStream } from "$lib/adapters/browser";
import { isRecord } from "$lib/utils/parse";

export function getMessageEventData(event: Event): string | undefined {
    if (event instanceof MessageEvent && typeof event.data === "string") {
        return event.data;
    }

    if (!isRecord(event)) {
        return undefined;
    }

    const data = event["data"];
    return typeof data === "string" ? data : undefined;
}

export interface SSEErrorInfo {
    status?: number;
    message?: string;
    readyState?: number;
    url?: string;
}

/** Read the fields an SSE error carries (an Event, or one relayed across tabs). */
export function parseErrorInfo(value: unknown): SSEErrorInfo {
    const info: SSEErrorInfo = {};
    if (!isRecord(value)) return info;
    if (typeof value.status === "number") info.status = value.status;
    if (typeof value.message === "string") info.message = value.message;
    if (typeof value.readyState === "number") info.readyState = value.readyState;
    if (typeof value.url === "string") info.url = value.url;
    return info;
}

export function extractErrorInfo(e: Event, es: SSEStream, url: string): SSEErrorInfo {
    return { ...parseErrorInfo(e), readyState: es.readyState, url };
}

export function formatErrorInfo(info: SSEErrorInfo): string {
    const parts: string[] = [];
    if (info.status !== undefined) parts.push(`status=${info.status.toString()}`);
    if (info.message !== undefined) parts.push(info.message);
    if (info.readyState !== undefined) parts.push(`readyState=${info.readyState.toString()}`);
    if (info.url !== undefined) parts.push(info.url);
    return parts.join(" ") || "unknown error";
}
