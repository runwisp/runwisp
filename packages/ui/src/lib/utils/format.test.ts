// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import {
    formatBytes,
    formatCalendarDate,
    formatClockTime,
    formatDayMonth,
    formatDuration,
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
        // Same-day branch produces `relative (HH:MM)` — assert on the colon
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
    it("advances as the supplied now advances", () => {
        const date = new Date("2024-01-01T12:00:00Z");
        expect(formatRelativeTime(date, new Date("2024-01-01T14:00:00Z"))).toBe(
            "about 2 hours ago",
        );
        expect(formatRelativeTime(date, new Date("2024-01-01T15:00:00Z"))).toBe(
            "about 3 hours ago",
        );
    });
});

describe("formatClockTime", () => {
    it("renders three numeric groups (hours, minutes, seconds)", () => {
        // 24-hour h:m:s — the seconds component is what sets it apart from formatTimeHM.
        expect(numericGroups(formatClockTime("2026-06-22T17:15:02Z"))).toBe(3);
    });
});

describe("formatTimeHM", () => {
    it("renders two numeric groups (hours, minutes) — no seconds", () => {
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
