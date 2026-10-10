// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Signed magnitude thresholds for Intl.RelativeTimeFormat, largest-first
// division. The buckets match date-fns' formatDistance.
const RELATIVE_DIVISIONS: [amount: number, unit: Intl.RelativeTimeFormatUnit][] = [
    [60, "seconds"],
    [60, "minutes"],
    [24, "hours"],
    [7, "days"],
    [4.34524, "weeks"],
    [12, "months"],
    [Number.POSITIVE_INFINITY, "years"],
];

const relativeTime = new Intl.RelativeTimeFormat(undefined, { numeric: "always" });

/** "N units ago" / "in N units" for `date` relative to `now`. */
export function formatRelativeTime(date: string | Date, now: Date = new Date()): string {
    let delta = (new Date(date).getTime() - now.getTime()) / 1000;
    for (const [amount, unit] of RELATIVE_DIVISIONS) {
        if (Math.abs(delta) < amount) {
            return relativeTime.format(Math.round(delta), unit);
        }
        delta /= amount;
    }
    return relativeTime.format(Math.round(delta), "years");
}

/** A locale date/time formatter for one fixed option set, built once. */
function dateFormatter(options: Intl.DateTimeFormatOptions): (date: string | Date) => string {
    const format = new Intl.DateTimeFormat(undefined, options);
    return (date) => format.format(new Date(date));
}

export const formatDateTime = dateFormatter({
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
});

/** Wall-clock time of day, seconds included, 24-hour, e.g. "17:15:02". */
export const formatClockTime = dateFormatter({
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
});

/** Calendar date without the time, e.g. "22 Jun 2026". */
export const formatCalendarDate = dateFormatter({
    year: "numeric",
    month: "short",
    day: "numeric",
});

/** Time of day, no seconds, 24-hour, e.g. "17:15". */
export const formatTimeHM = dateFormatter({ hour: "2-digit", minute: "2-digit", hour12: false });

/** Day and month, no year, e.g. "22 Jun". */
export const formatDayMonth = dateFormatter({ month: "short", day: "numeric" });

export const formatFullDateTime = dateFormatter({
    year: "numeric",
    month: "short",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
});

const formatShortTime = dateFormatter({ hour: "numeric", minute: "2-digit", hour12: false });

export function formatRelativeTimeWithAbsolute(
    dateStr: string | Date,
    now: Date = new Date(),
): string {
    const date = new Date(dateStr);
    const diffDays = Math.abs(now.getTime() - date.getTime()) / (1000 * 60 * 60 * 24);
    const absolute = diffDays < 1 ? formatShortTime(date) : formatDayMonth(date);
    return `${formatRelativeTime(date, now)} (${absolute})`;
}

export function formatBytes(bytes: number): string {
    const units: [string, ...string[]] = ["B", "KB", "MB", "GB", "TB", "PB"];
    let value = bytes;
    let unitIndex = 0;
    while (value >= 1024 && unitIndex < units.length - 1) {
        value /= 1024;
        unitIndex++;
    }
    const unit = units[unitIndex] ?? "B";
    const digits = value < 10 && unitIndex > 0 ? 1 : 0;
    return `${new Intl.NumberFormat("en", { maximumFractionDigits: digits }).format(value)} ${unit}`;
}

export function formatDuration(ms: number): string {
    if (ms < 1000) return String(ms) + "ms";
    const s = Math.floor(ms / 1000);
    if (s < 60) return String(s) + "s";
    const m = Math.floor(s / 60);
    const rem = s % 60;
    if (m < 60) return rem > 0 ? String(m) + "m " + String(rem) + "s" : String(m) + "m";
    const h = Math.floor(m / 60);
    const remM = m % 60;
    return remM > 0 ? String(h) + "h " + String(remM) + "m" : String(h) + "h";
}

// Largest first, so formatDurationExact can peel units off greedily.
const DURATION_UNITS: [unit: string, ms: number][] = [
    ["d", 86_400_000],
    ["h", 3_600_000],
    ["m", 60_000],
    ["s", 1000],
    ["ms", 1],
];

// Every unit name parseDuration takes, in ms. The `ms` package's set.
const UNIT_ALIASES: [ms: number, names: string[]][] = [
    [1, ["ms", "msec", "msecs", "millisecond", "milliseconds"]],
    [1000, ["s", "sec", "secs", "second", "seconds"]],
    [60_000, ["m", "min", "mins", "minute", "minutes"]],
    [3_600_000, ["h", "hr", "hrs", "hour", "hours"]],
    [86_400_000, ["d", "day", "days"]],
    [604_800_000, ["w", "week", "weeks"]],
    [31_557_600_000, ["y", "yr", "yrs", "year", "years"]],
];
const UNIT_MS = new Map(UNIT_ALIASES.flatMap(([size, names]) => names.map((n) => [n, size])));

/**
 * parseDuration reads a duration into milliseconds. It takes what the `ms`
 * package does ("500" as milliseconds, ".5h", "2 hours", "30 sec", "1w", "1y",
 * any case) plus compound forms: "5m 30s", "1h30m". Returns null when any part
 * isn't a number with a unit (only a lone number may go without one).
 */
export function parseDuration(text: string): number | null {
    const s = text.trim().toLowerCase();
    // Bounded like `ms`, which also keeps the number pattern's backtracking cheap.
    if (!s || s.length > 100) return null;
    const segment = /(-?\d*\.?\d+)\s*([a-z]*)\s*/y;
    let total = 0;
    let segments = 0;
    let bare = false;
    while (segment.lastIndex < s.length) {
        const match = segment.exec(s);
        if (!match) return null;
        const [, amount = "", unit = ""] = match;
        const size = unit ? UNIT_MS.get(unit) : 1;
        if (size === undefined) return null;
        bare ||= !unit;
        total += Number(amount) * size;
        segments++;
    }
    if (bare && segments > 1) return null;
    return Math.round(total);
}

/** formatDurationExact renders milliseconds as "1h 5m 30s", keeping every non-zero unit. */
export function formatDurationExact(ms: number): string {
    const parts: string[] = [];
    let rest = Math.round(ms);
    for (const [unit, size] of DURATION_UNITS) {
        const amount = Math.floor(rest / size);
        if (amount > 0) {
            parts.push(String(amount) + unit);
            rest -= amount * size;
        }
    }
    return parts.length > 0 ? parts.join(" ") : "0ms";
}
