// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

// ErrorEvent is not available in the Node test environment; provide a minimal polyfill
// so that `instanceof ErrorEvent` in the production code doesn't throw.
if (typeof Reflect.get(globalThis, "ErrorEvent") === "undefined") {
    Reflect.set(
        globalThis,
        "ErrorEvent",
        class ErrorEventPolyfill extends Event {
            readonly message: string;
            constructor(type: string, init?: { message?: string }) {
                super(type);
                this.message = init?.message ?? "";
            }
        },
    );
}
import {
    getEventSourceErrorDetails,
    getMessageEventData,
    formatErrorInfo,
    extractErrorInfo,
    type SSEErrorInfo,
} from "./event-source";
import type { SSEStream } from "$lib/adapters/browser";

// ─── getEventSourceErrorDetails ──────────────────────────────────────────────

describe("getEventSourceErrorDetails", () => {
    it("returns empty object for non-object event", () => {
        const result = getEventSourceErrorDetails(new Event("error"));
        expect(result.status).toBeUndefined();
        expect(result.message).toBeUndefined();
    });

    it("extracts numeric status from record-like event", () => {
        const evt = Object.assign(new Event("error"), { status: 503 });
        const result = getEventSourceErrorDetails(evt);
        expect(result.status).toBe(503);
    });

    it("extracts string message from record-like event", () => {
        const evt = Object.assign(new Event("error"), { message: "connection refused" });
        const result = getEventSourceErrorDetails(evt);
        expect(result.message).toBe("connection refused");
    });

    it("extracts message from ErrorEvent when no message in record", () => {
        const evt = new ErrorEvent("error", { message: "network error" });
        const result = getEventSourceErrorDetails(evt);
        expect(result.message).toBe("network error");
    });

    it("extracts both status and message", () => {
        const evt = Object.assign(new Event("error"), { status: 401, message: "unauthorized" });
        const result = getEventSourceErrorDetails(evt);
        expect(result.status).toBe(401);
        expect(result.message).toBe("unauthorized");
    });

    it("does not include status in result when not present", () => {
        const result = getEventSourceErrorDetails(new Event("error"));
        expect(Object.keys(result)).not.toContain("status");
    });

    it("does not extract ErrorEvent message when record already has message set", () => {
        // message is set from the record extraction, so the ErrorEvent branch is skipped
        const evt = Object.assign(new ErrorEvent("error", { message: "polyfill" }), {
            message: "from-record",
        });
        const result = getEventSourceErrorDetails(evt);
        expect(result.message).toBe("from-record");
    });

    it("ErrorEvent with empty message: record path gets empty string (not undefined)", () => {
        // The polyfill exposes `message` as a string property so the record
        // extraction branch (typeof rawMessage === "string") sets message = "".
        // The ErrorEvent-specific branch is therefore skipped via short-circuit.
        const evt = new ErrorEvent("error", { message: "" });
        const result = getEventSourceErrorDetails(evt);
        expect(result.message).toBe("");
    });
});

// ─── getMessageEventData ──────────────────────────────────────────────────────

describe("getMessageEventData", () => {
    it("returns data from MessageEvent with string data", () => {
        const evt = new MessageEvent("message", { data: '{"key":"val"}' });
        expect(getMessageEventData(evt)).toBe('{"key":"val"}');
    });

    it("returns undefined for plain Event (not a record)", () => {
        // A plain Event with no 'data' property
        const evt = new Event("error");
        expect(getMessageEventData(evt)).toBeUndefined();
    });

    it("extracts data from record-like event (non-MessageEvent)", () => {
        const evt = Object.assign(new Event("message"), { data: "hello world" });
        expect(getMessageEventData(evt)).toBe("hello world");
    });

    it("returns undefined for record event without data property", () => {
        const evt = Object.assign(new Event("message"), { other: "prop" });
        expect(getMessageEventData(evt)).toBeUndefined();
    });
});

// ─── formatErrorInfo ─────────────────────────────────────────────────────────

describe("formatErrorInfo", () => {
    it("formats all fields present", () => {
        const info: SSEErrorInfo = {
            status: 503,
            message: "Service Unavailable",
            readyState: 2,
            url: "http://localhost/events",
        };
        const result = formatErrorInfo(info);
        expect(result).toContain("503");
        expect(result).toContain("Service Unavailable");
        expect(result).toContain("readyState=2");
        expect(result).toContain("http://localhost/events");
    });

    it("formats with only status", () => {
        const result = formatErrorInfo({ status: 404 });
        expect(result).toBe("status=404");
    });

    it("formats with only message", () => {
        const result = formatErrorInfo({ message: "timeout" });
        expect(result).toBe("timeout");
    });

    it("returns 'unknown error' when all fields undefined", () => {
        const result = formatErrorInfo({});
        expect(result).toBe("unknown error");
    });

    it("formats with only readyState", () => {
        const result = formatErrorInfo({ readyState: 0 });
        expect(result).toContain("readyState=0");
    });

    it("formats with only url", () => {
        const result = formatErrorInfo({ url: "http://localhost/sse" });
        expect(result).toBe("http://localhost/sse");
    });
});

// ─── extractErrorInfo ─────────────────────────────────────────────────────────

describe("extractErrorInfo", () => {
    it("includes readyState and url from the SSEStream", () => {
        const mockStream: SSEStream = {
            readyState: 1,
            onopen: null,
            onerror: null,
            onmessage: null,
            close: () => {},
            addEventListener: () => {},
        };
        const evt = Object.assign(new Event("error"), { status: 500 });
        const info = extractErrorInfo(evt, mockStream, "http://localhost/sse");
        expect(info.readyState).toBe(1);
        expect(info.url).toBe("http://localhost/sse");
        expect(info.status).toBe(500);
    });

    it("omits status/message when not present in event", () => {
        const mockStream: SSEStream = {
            readyState: 3,
            onopen: null,
            onerror: null,
            onmessage: null,
            close: () => {},
            addEventListener: () => {},
        };
        const info = extractErrorInfo(new Event("error"), mockStream, "http://example.com/sse");
        expect(info.status).toBeUndefined();
        expect(info.message).toBeUndefined();
        expect(info.readyState).toBe(3);
    });

    it("includes message when event has a string message property", () => {
        const mockStream: SSEStream = {
            readyState: 0,
            onopen: null,
            onerror: null,
            onmessage: null,
            close: () => {},
            addEventListener: () => {},
        };
        const evt = Object.assign(new Event("error"), { message: "connection refused" });
        const info = extractErrorInfo(evt, mockStream, "http://localhost/sse");
        expect(info.message).toBe("connection refused");
    });
});
