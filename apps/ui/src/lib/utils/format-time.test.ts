// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { relative } from "./format-time";

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

    // Each row pins one bucket; pair (boundary, midrange) per bucket — extra
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
