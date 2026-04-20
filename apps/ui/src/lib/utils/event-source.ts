// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export interface EventSourceErrorDetails {
    status?: number;
    message?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
    return typeof value === "object" && value !== null;
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

    if (message === undefined && event instanceof ErrorEvent && event.message) {
        message = event.message;
    }

    return {
        ...(status !== undefined && { status }),
        ...(message !== undefined && { message }),
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
