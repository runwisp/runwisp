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

/** Wall-clock time of day, seconds included, 24-hour — e.g. "17:15:02". */
export const formatClockTime = dateFormatter({
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
});

/** Calendar date without the time — e.g. "22 Jun 2026". */
export const formatCalendarDate = dateFormatter({
    year: "numeric",
    month: "short",
    day: "numeric",
});

/** Time of day, no seconds, 24-hour — e.g. "17:15". */
export const formatTimeHM = dateFormatter({ hour: "2-digit", minute: "2-digit", hour12: false });

/** Day and month, no year — e.g. "22 Jun". */
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
