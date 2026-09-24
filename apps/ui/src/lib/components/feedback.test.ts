// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import {
    FEEDBACK_URL,
    MIN_UPTIME_MS,
    SNOOZE_MS,
    parseStoredState,
    sendFeedback,
    shouldAutoOpen,
} from "./feedback.js";

const NOW = 1_800_000_000_000;
const up = (ms: number) => ({ startedAt: NOW - ms, now: NOW, checkUpdates: true });
const fresh = { submitted: false };

describe("shouldAutoOpen", () => {
    it("waits for an hour of uptime", () => {
        expect(shouldAutoOpen(fresh, up(MIN_UPTIME_MS - 1))).toBe(false);
        expect(shouldAutoOpen(fresh, up(MIN_UPTIME_MS))).toBe(true);
    });

    it("stays off when check_updates is off or the start time is unknown", () => {
        expect(shouldAutoOpen(fresh, { ...up(2 * MIN_UPTIME_MS), checkUpdates: false })).toBe(
            false,
        );
        expect(shouldAutoOpen(fresh, { startedAt: 0, now: NOW, checkUpdates: true })).toBe(false);
    });

    it("never reopens after a rating", () => {
        expect(shouldAutoOpen({ ...fresh, submitted: true }, up(2 * MIN_UPTIME_MS))).toBe(false);
    });

    it("re-asks a month after each close", () => {
        const closedOnce = { submitted: false, dismissedAt: NOW - SNOOZE_MS + 1 };
        expect(shouldAutoOpen(closedOnce, up(2 * MIN_UPTIME_MS))).toBe(false);
        expect(
            shouldAutoOpen({ ...closedOnce, dismissedAt: NOW - SNOOZE_MS }, up(2 * MIN_UPTIME_MS)),
        ).toBe(true);
    });
});

describe("parseStoredState", () => {
    it("falls back on missing or corrupt storage", () => {
        expect(parseStoredState(null)).toEqual(fresh);
        expect(parseStoredState("{nope")).toEqual(fresh);
        expect(parseStoredState('{"submitted":"nope"}')).toEqual(fresh);
    });

    it("round-trips a stored state", () => {
        const state = { submitted: false, dismissedAt: 42 };
        expect(parseStoredState(JSON.stringify(state))).toEqual(state);
    });
});

describe("sendFeedback", () => {
    const respond = (status: number) =>
        vi.fn<typeof fetch>().mockResolvedValue(new Response("{}", { status }));

    it("posts the body plus the version to concierge, with no referrer", async () => {
        const fetchFn = respond(201);
        expect(await sendFeedback({ rating: 5 }, "1.0.1", fetchFn)).toEqual({ ok: true });
        const [url, init] = fetchFn.mock.calls[0] ?? [];
        expect(url).toBe(FEEDBACK_URL);
        expect(init?.body).toBe(JSON.stringify({ rating: 5, version: "1.0.1" }));
        expect(init?.referrerPolicy).toBe("no-referrer");
    });

    it("maps the rate limit to a friendly message", async () => {
        const res = await sendFeedback({ message: "hi" }, "1.0.1", respond(429));
        expect(res.ok ? "" : res.error).toContain("plenty for today");
    });

    it("reports other statuses and network failures", async () => {
        expect(await sendFeedback({ rating: 1 }, "1.0.1", respond(502))).toEqual({
            ok: false,
            error: "Something went wrong (HTTP 502).",
        });
        const offline = vi.fn<typeof fetch>().mockRejectedValue(new TypeError("Failed to fetch"));
        expect(await sendFeedback({ rating: 1 }, "1.0.1", offline)).toEqual({
            ok: false,
            error: "Couldn't reach runwisp.com from this browser.",
        });
    });
});
