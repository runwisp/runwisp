// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { z } from "zod";
import { phrase, relative, sparkline } from "./notification-rhythm";
import vectorsRaw from "./__rhythm_vectors.json";

const vectorSchema = z.object({
    name: z.string(),
    count: z.number(),
    createdAt: z.string(),
    lastOccurredAt: z.string(),
    occurrences_unix_ms: z.array(z.number()),
    now_unix_ms: z.number(),
    window_ms: z.number(),
    cells: z.number(),
    expected_phrase: z.string(),
    expected_sparkline: z.string(),
});

const vectors = z.array(vectorSchema).parse(vectorsRaw);

describe("notification rhythm parity (Go ↔ TS)", () => {
    for (const v of vectors) {
        it(v.name, () => {
            const now = new Date(v.now_unix_ms);
            const occ = v.occurrences_unix_ms.map((ms) => new Date(ms));
            const phraseOut = phrase({
                count: v.count,
                createdAt: v.createdAt,
                lastOccurredAt: v.lastOccurredAt,
                occurrences: occ,
                now,
            });
            expect(phraseOut).toBe(v.expected_phrase);
            if (v.expected_sparkline !== "") {
                const sparkOut = sparkline(occ, now, v.window_ms, v.cells);
                expect(sparkOut).toBe(v.expected_sparkline);
            }
        });
    }
});

// ─── phrase: extra branch coverage ───────────────────────────────────────────

describe("phrase extra branches", () => {
    const NOW = new Date("2024-06-15T12:00:00Z");

    it("uses current time when input.now is omitted", () => {
        const result = phrase({
            count: 1,
            createdAt: new Date(Date.now() - 10_000).toISOString(),
            lastOccurredAt: new Date(Date.now() - 5_000).toISOString(),
            occurrences: [],
        });
        expect(result).toBe("just now");
    });

    it("all occurrences within 1hr → 'in the last hour'", () => {
        const latest = new Date(NOW.getTime() - 10 * 60 * 1000);
        const older = new Date(NOW.getTime() - 20 * 60 * 1000);
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 30 * 60 * 1000).toISOString(),
            lastOccurredAt: latest.toISOString(),
            occurrences: [latest, older],
            now: NOW,
        });
        expect(result).toContain("in the last hour");
    });

    it("some occ older than 1hr but all within day → 'today'", () => {
        const latest = new Date(NOW.getTime() - 30 * 60 * 1000);
        const older = new Date(NOW.getTime() - 90 * 60 * 1000);
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 2 * 60 * 60 * 1000).toISOString(),
            lastOccurredAt: latest.toISOString(),
            occurrences: [latest, older],
            now: NOW,
        });
        expect(result).toContain("today");
    });

    it("formatSpan: minutes (span < 1h, empty occ → allWithin always false)", () => {
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 30 * 60 * 1000).toISOString(),
            lastOccurredAt: NOW.toISOString(),
            occurrences: [],
            now: NOW,
        });
        expect(result).toBe("2× over 30m, latest just now");
    });

    it("formatSpan: hours (1h ≤ span < 24h)", () => {
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 3 * 60 * 60 * 1000).toISOString(),
            lastOccurredAt: NOW.toISOString(),
            occurrences: [],
            now: NOW,
        });
        expect(result).toBe("2× over 3h, latest just now");
    });

    it("formatSpan: days (24h ≤ span < 7d)", () => {
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 3 * 24 * 60 * 60 * 1000).toISOString(),
            lastOccurredAt: NOW.toISOString(),
            occurrences: [],
            now: NOW,
        });
        expect(result).toBe("2× over 3d, latest just now");
    });

    it("span >= WEEK → 'since'", () => {
        const created = new Date(NOW.getTime() - 14 * 24 * 60 * 60 * 1000);
        const result = phrase({
            count: 2,
            createdAt: created.toISOString(),
            lastOccurredAt: NOW.toISOString(),
            occurrences: [],
            now: NOW,
        });
        expect(result).toContain("since");
    });

    it("occ older than 24h → allWithin(DAY_MS) false → falls through to formatSpan", () => {
        const latest = new Date(NOW.getTime() - 30 * 60 * 1000);
        const ancient = new Date(NOW.getTime() - 25 * 60 * 60 * 1000);
        const result = phrase({
            count: 2,
            createdAt: new Date(NOW.getTime() - 2 * 60 * 60 * 1000).toISOString(),
            lastOccurredAt: latest.toISOString(),
            occurrences: [latest, ancient],
            now: NOW,
        });
        expect(result).toBe("2× over 2h, latest 30m ago");
    });
});

// ─── sparkline: extra branch coverage ────────────────────────────────────────

describe("sparkline extra branches", () => {
    const NOW = new Date("2024-06-15T12:00:00Z");
    const WINDOW = 60 * 60 * 1000; // 1h

    it("returns '' for empty occurrences", () => {
        expect(sparkline([], NOW, WINDOW)).toBe("");
    });

    it("returns '' for windowMs = 0", () => {
        expect(sparkline([new Date(NOW.getTime() - 1000)], NOW, 0)).toBe("");
    });

    it("returns '' for windowMs < 0", () => {
        expect(sparkline([new Date(NOW.getTime() - 1000)], NOW, -1)).toBe("");
    });

    it("uses 8 cells when cells = 0", () => {
        const occ = [new Date(NOW.getTime() - 1000)];
        expect(sparkline(occ, NOW, WINDOW, 0)).toHaveLength(8);
    });

    it("returns '' when all occurrences are outside the window (max = 0)", () => {
        const occ = [new Date(NOW.getTime() - 2 * WINDOW - 1)];
        expect(sparkline(occ, NOW, WINDOW)).toBe("");
    });

    it("skips occurrence at window boundary, renders the one inside", () => {
        const outside = new Date(NOW.getTime() - WINDOW - 1);
        const inside = new Date(NOW.getTime() - 1000);
        const result = sparkline([outside, inside], NOW, WINDOW);
        expect(result).toHaveLength(8);
        expect(result).not.toBe("");
    });
});

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const MONTH = 30 * DAY;
const YEAR = 365 * DAY;

const NOW = new Date("2024-06-15T12:00:00Z");

function ago(ms: number): Date {
    return new Date(NOW.getTime() - ms);
}

describe("relative", () => {
    describe("string and Date input handling", () => {
        it.each([
            ["ISO string parses", ago(10 * SECOND).toISOString(), "just now"],
            ["invalid date string", "not-a-date", ""],
            ["empty string", "", ""],
        ])("%s", (_label, input, expected) => {
            expect(relative(input, NOW)).toBe(expected);
        });

        it("returns empty string for a Date constructed from NaN", () => {
            expect(relative(new Date(NaN), NOW)).toBe("");
        });
    });

    // Each row pins one bucket; pair (boundary, midrange) per bucket, extra
    // duplicate rows did not add coverage.
    it.each([
        ["0ms ago", 0, "just now"],
        ["29s ago (high boundary of just-now)", 29 * SECOND, "just now"],
        ["30s ago (low boundary of seconds bucket)", 30 * SECOND, "30s ago"],
        ["59s ago (high boundary of seconds bucket)", 59 * SECOND, "59s ago"],
        ["1m ago (low boundary of minutes bucket)", MINUTE, "1m ago"],
        ["59m ago (high boundary of minutes bucket)", 59 * MINUTE, "59m ago"],
        ["1h ago (low boundary of hours bucket)", HOUR, "1h ago"],
        ["23h ago (high boundary of hours bucket)", 23 * HOUR, "23h ago"],
        ["1d ago (low boundary of yesterday bucket)", DAY, "yesterday"],
        ["just under 2d ago (high boundary of yesterday)", 2 * DAY - SECOND, "yesterday"],
        ["2d ago (low boundary of days bucket)", 2 * DAY, "2d ago"],
        ["29d ago (high boundary of days bucket)", 29 * DAY, "29d ago"],
        ["31d ago (low boundary of months bucket)", 31 * DAY, "1mo ago"],
        ["59d ago (mid months bucket)", 59 * DAY, "1mo ago"],
        ["65d ago (mid months bucket)", 65 * DAY, "2mo ago"],
        ["11mo ago", 11 * MONTH, "11mo ago"],
        ["366d ago (low boundary of years bucket)", 366 * DAY, "1y ago"],
        ["exactly 1y ago", YEAR, "1y ago"],
        ["800d ago", 800 * DAY, "2y ago"],
        ["1200d ago", 1200 * DAY, "3y ago"],
    ])("%s", (_label, msAgo, expected) => {
        expect(relative(ago(msAgo), NOW)).toBe(expected);
    });
});
