// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { formatBytes, formatDuration, formatRelativeTimeWithAbsolute } from "./format.js";

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
        // Older-than-day branch produces a parenthesised month/day suffix.
        // Intl.DateTimeFormat orders the parts per locale (en-US → "May 15",
        // most others → "15 May"), so accept either ordering — the point of
        // the assertion is that the suffix has no time colon.
        expect(r).toMatch(/\((?:\d{1,2} [A-Za-z]{3,}|[A-Za-z]{3,} \d{1,2})\)$/);
        expect(r).not.toMatch(/\(\d{1,2}:\d{2}\)$/);
    });
});
