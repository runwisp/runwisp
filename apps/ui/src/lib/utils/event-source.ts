// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { SSEStream } from "$lib/adapters/browser";

export interface EventSourceErrorDetails {
    status?: number;
    message?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && Boolean(value);
}

export function getEventSourceErrorDetails(event: Event): EventSourceErrorDetails {
    let status: number | undefined;
    let message: string | undefined;

    if (isRecord(event)) {
        const rawStatus = event["status"];
        if (typeof rawStatus === "number") {
            status = rawStatus;
        }

        const rawMessage = event["message"];
        if (typeof rawMessage === "string") {
            message = rawMessage;
        }
    }

    if (typeof message === "undefined" && event instanceof ErrorEvent && event.message) {
        message = event.message;
    }

    return {
        ...(typeof status !== "undefined" && { status }),
        ...(typeof message !== "undefined" && { message }),
    };
}

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

export function extractErrorInfo(e: Event, es: SSEStream, url: string): SSEErrorInfo {
    const { status, message } = getEventSourceErrorDetails(e);
    return {
        ...(typeof status !== "undefined" && { status }),
        ...(typeof message !== "undefined" && { message }),
        readyState: es.readyState,
        url,
    };
}

export function formatErrorInfo(info: SSEErrorInfo): string {
    const parts: string[] = [];
    if (typeof info.status !== "undefined") parts.push(`status=${info.status.toString()}`);
    if (typeof info.message !== "undefined") parts.push(info.message);
    if (typeof info.readyState !== "undefined")
        parts.push(`readyState=${info.readyState.toString()}`);
    if (typeof info.url !== "undefined") parts.push(info.url);
    return parts.join(" ") || "unknown error";
}
