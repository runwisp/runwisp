// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
    formatBytes,
    formatCalendarDate,
    formatClockTime,
    formatDayMonth,
    formatDuration,
    formatDurationExact,
    parseDuration,
    formatRelativeCompact,
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    formatTimeHM,
} from "./format.js";

// The clock/date helpers below delegate to toLocaleTimeString / toLocaleDateString,
// whose exact rendering is locale- and timezone-dependent. We assert the
// locale-invariant *shape* of each format (how many numeric groups, year present
// or not) rather than a literal string, mirroring formatRelativeTimeWithAbsolute.
const numericGroups = (s: string): number => (s.match(/\d+/g) ?? []).length;

describe("formatBytes", () => {
    it("formats 0 bytes", () => {
        expect(formatBytes(0)).toBe("0 B");
    });
    it("formats bytes under 1 KB", () => {
        expect(formatBytes(500)).toBe("500 B");
    });
    it("formats exactly 1 KB", () => {
        expect(formatBytes(1024)).toBe("1 KB");
    });
    it("formats fractional KB when value < 10 (one decimal)", () => {
        expect(formatBytes(1024 * 1.5)).toBe("1.5 KB");
    });
    it("formats KB >= 10 without decimal", () => {
        expect(formatBytes(1024 * 10)).toBe("10 KB");
    });
    it("formats MB", () => {
        expect(formatBytes(1024 ** 2)).toBe("1 MB");
    });
    it("formats GB", () => {
        expect(formatBytes(1024 ** 3)).toBe("1 GB");
    });
    it("formats TB", () => {
        expect(formatBytes(1024 ** 4)).toBe("1 TB");
    });
    it("formats PB (largest unit)", () => {
        expect(formatBytes(1024 ** 5)).toBe("1 PB");
    });
});

describe("formatDuration", () => {
    it("returns ms for 0", () => {
        expect(formatDuration(0)).toBe("0ms");
    });
    it("returns ms for < 1 second", () => {
        expect(formatDuration(999)).toBe("999ms");
    });
    it("returns seconds for exactly 1s", () => {
        expect(formatDuration(1000)).toBe("1s");
    });
    it("returns seconds for 59s", () => {
        expect(formatDuration(59_000)).toBe("59s");
    });
    it("returns minutes for exactly 1m (no remaining seconds)", () => {
        expect(formatDuration(60_000)).toBe("1m");
    });
    it("returns minutes + seconds when remainder > 0", () => {
        expect(formatDuration(90_000)).toBe("1m 30s");
    });
    it("returns exactly 59m", () => {
        expect(formatDuration(59 * 60_000)).toBe("59m");
    });
    it("returns hours for exactly 1h (no remaining minutes)", () => {
        expect(formatDuration(3_600_000)).toBe("1h");
    });
    it("returns hours + minutes when remainder > 0", () => {
        expect(formatDuration(5_400_000)).toBe("1h 30m");
    });
    it("returns hours only when remM = 0", () => {
        expect(formatDuration(2 * 3_600_000)).toBe("2h");
    });
});

describe("formatRelativeTimeWithAbsolute", () => {
    it("uses time-only format (HH:MM) for dates within the same day", () => {
        const recent = new Date(Date.now() - 30 * 60_000).toISOString();
        const r = formatRelativeTimeWithAbsolute(recent);
        // Same-day branch produces `relative (HH:MM)`, assert on the colon
        // that only appears inside the time format.
        expect(r).toMatch(/\(\d{1,2}:\d{2}\)$/);
    });
    it("uses month-day format for dates more than 1 day ago", () => {
        const old = new Date(Date.now() - 2 * 24 * 60 * 60_000).toISOString();
        const r = formatRelativeTimeWithAbsolute(old);
        // Older-than-day branch produces a parenthesised calendar-date suffix.
        // Locales render { month: "short", day: "numeric" } very differently
        // ("May 15" / "15 May" / "16. 5." / "5月15日"), so the locale-invariant
        // contract we assert is: parens contain a day digit and no time colon.
        expect(r).toMatch(/\([^)]*\d[^)]*\)$/);
        expect(r).not.toMatch(/\(\d{1,2}:\d{2}\)$/);
    });
    it("adds the year when the date falls in another year", () => {
        const now = new Date(2026, 9, 20, 12);
        expect(formatRelativeTimeWithAbsolute(new Date(2028, 1, 29), now)).toMatch(/2028\)$/);
        expect(formatRelativeTimeWithAbsolute(new Date(2026, 11, 1), now)).not.toMatch(/2026/);
    });
    it("computes the relative part against the supplied now", () => {
        const date = new Date("2024-01-01T12:00:00Z");
        const twoHoursBefore = formatRelativeTimeWithAbsolute(
            date,
            new Date("2024-01-01T10:00:00Z"),
        );
        const oneHourBefore = formatRelativeTimeWithAbsolute(
            date,
            new Date("2024-01-01T11:00:00Z"),
        );
        expect(twoHoursBefore).toMatch(/^in 2 hours/);
        expect(oneHourBefore).toMatch(/^in 1 hour/);
    });
    it("picks the day-boundary branch against the supplied now", () => {
        const date = new Date("2024-01-01T12:00:00Z");
        // 2 days after the date → calendar-date suffix, not HH:MM.
        const r = formatRelativeTimeWithAbsolute(date, new Date("2024-01-03T12:00:00Z"));
        expect(r).not.toMatch(/\(\d{1,2}:\d{2}\)$/);
    });
});

describe("formatRelativeTime", () => {
    const now = new Date("2024-01-10T12:00:00Z");
    const at = (seconds: number): Date => new Date(now.getTime() + seconds * 1000);

    it("advances as the supplied now advances", () => {
        const date = new Date("2024-01-01T12:00:00Z");
        expect(formatRelativeTime(date, new Date("2024-01-01T14:00:00Z"))).toBe("2 hours ago");
        expect(formatRelativeTime(date, new Date("2024-01-01T15:00:00Z"))).toBe("3 hours ago");
    });

    it("always shows a number by default", () => {
        expect(formatRelativeTime(at(0), now)).toBe("in 0 seconds");
        expect(formatRelativeTime(at(-86_400), now)).toBe("1 day ago");
        expect(formatRelativeTime(at(86_400), now)).toBe("in 1 day");
    });

    it.each([
        [0, "now"],
        [-16, "16 seconds ago"],
        [16, "in 16 seconds"],
        [-60, "1 minute ago"],
        [-5 * 60, "5 minutes ago"],
        [-3600, "1 hour ago"],
        [2 * 3600, "in 2 hours"],
        [-86_400, "yesterday"],
        [86_400, "tomorrow"],
        [-3 * 86_400, "3 days ago"],
    ])("numeric auto: %is from now reads %s", (seconds, expected) => {
        expect(formatRelativeTime(at(seconds), now, { numeric: "auto" })).toBe(expected);
    });

    it("accepts a timestamp, an ISO string or a Date", () => {
        const date = at(-120);
        expect(formatRelativeTime(date.getTime(), now)).toBe("2 minutes ago");
        expect(formatRelativeTime(date.toISOString(), now)).toBe("2 minutes ago");
        expect(formatRelativeTime(date, now)).toBe("2 minutes ago");
    });

    it("throws on an invalid date", () => {
        expect(() => formatRelativeTime("not a date", now)).toThrow(RangeError);
        expect(() => formatRelativeTime("not a date", now, { numeric: "auto" })).toThrow(
            RangeError,
        );
    });
});

describe("formatRelativeCompact", () => {
    const now = new Date("2024-01-10T12:00:00Z");
    const ago = (seconds: number): number => now.getTime() - seconds * 1000;

    it.each([
        [0, "0s"],
        [16.9, "16s"],
        [59, "59s"],
        [60, "1m"],
        [3 * 60 + 59, "3m"],
        [59 * 60, "59m"],
        [3600, "1h"],
        [23 * 3600 + 3599, "23h"],
        [86_400, "1d"],
        [45 * 86_400, "45d"],
        [400 * 86_400, "400d"],
    ])("%is ago reads %s", (seconds, expected) => {
        expect(formatRelativeCompact(ago(seconds), now)).toBe(expected);
    });

    it("clamps a future date to 0s", () => {
        expect(formatRelativeCompact(ago(-90), now)).toBe("0s");
    });

    it("accepts an ISO string or a Date", () => {
        expect(formatRelativeCompact(new Date(ago(180)).toISOString(), now)).toBe("3m");
        expect(formatRelativeCompact(new Date(ago(180)), now)).toBe("3m");
    });

    it("does not guard an invalid date", () => {
        // Same as the station helper it replaces; callers pass server timestamps.
        expect(formatRelativeCompact("not a date", now)).toBe("NaNd");
    });
});

describe("formatClockTime", () => {
    it("renders three numeric groups (hours, minutes, seconds)", () => {
        // 24-hour h:m:s, the seconds component is what sets it apart from formatTimeHM.
        expect(numericGroups(formatClockTime("2026-06-22T17:15:02Z"))).toBe(3);
    });
});

describe("formatTimeHM", () => {
    it("renders two numeric groups (hours, minutes), no seconds", () => {
        expect(numericGroups(formatTimeHM("2026-06-22T17:15:02Z"))).toBe(2);
    });
});

describe("formatCalendarDate", () => {
    it("includes the four-digit year", () => {
        // Midday UTC keeps the calendar year stable across every timezone.
        expect(formatCalendarDate("2026-06-22T12:00:00Z")).toContain("2026");
    });
});

describe("formatDayMonth", () => {
    it("includes a day digit but omits the year", () => {
        const r = formatDayMonth("2026-06-22T12:00:00Z");
        expect(r).toMatch(/\d/);
        expect(r).not.toContain("2026");
    });
});

describe("parseDuration", () => {
    it("reads the placeholder's own format", () => {
        expect(parseDuration("5m 30s")).toBe(330_000);
    });

    it("reads compact, fractional and sub-second forms", () => {
        expect(parseDuration("1h30m")).toBe(5_400_000);
        expect(parseDuration("1.5h")).toBe(5_400_000);
        expect(parseDuration("250ms")).toBe(250);
        expect(parseDuration("2d")).toBe(172_800_000);
    });

    it("reads what the ms package did", () => {
        expect(parseDuration("500")).toBe(500);
        expect(parseDuration("1w")).toBe(604_800_000);
        expect(parseDuration("1y")).toBe(31_557_600_000);
        expect(parseDuration("2 hours")).toBe(7_200_000);
        expect(parseDuration("30 sec")).toBe(30_000);
        expect(parseDuration("5 minutes")).toBe(300_000);
        expect(parseDuration("5M")).toBe(300_000);
        expect(parseDuration(".5h")).toBe(1_800_000);
        expect(parseDuration(" 1 Hour 30 mins ")).toBe(5_400_000);
    });

    it("rejects text that isn't number+unit throughout", () => {
        expect(parseDuration("")).toBeNull();
        expect(parseDuration("5x")).toBeNull();
        expect(parseDuration("5m abc")).toBeNull();
        expect(parseDuration("5 30s")).toBeNull();
        expect(parseDuration("1.5.5")).toBeNull();
        expect(parseDuration("9".repeat(101))).toBeNull();
    });
});

describe("formatDurationExact", () => {
    it("keeps every non-zero unit instead of rounding", () => {
        expect(formatDurationExact(330_000)).toBe("5m 30s");
        expect(formatDurationExact(3_930_000)).toBe("1h 5m 30s");
        expect(formatDurationExact(0)).toBe("0ms");
    });

    it("round-trips through parseDuration", () => {
        expect(parseDuration(formatDurationExact(90_061_001))).toBe(90_061_001);
    });
});
