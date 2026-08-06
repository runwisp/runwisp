// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Signed magnitude thresholds for Intl.RelativeTimeFormat, largest-first
// division. Mirrors the buckets date-fns' formatDistance used to pick.
const RELATIVE_DIVISIONS: [amount: number, unit: Intl.RelativeTimeFormatUnit][] = [
    [60, "seconds"],
    [60, "minutes"],
    [24, "hours"],
    [7, "days"],
    [4.34524, "weeks"],
    [12, "months"],
    [Number.POSITIVE_INFINITY, "years"],
];

// formatDistance renders a signed date delta as "N units ago" / "in N units"
// using the platform Intl formatter. Negative delta (date before base) reads as
// past, positive as future.
function formatDistance(date: Date, base: Date): string {
    const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "always" });
    let delta = (date.getTime() - base.getTime()) / 1000;
    for (const [amount, unit] of RELATIVE_DIVISIONS) {
        if (Math.abs(delta) < amount) {
            return rtf.format(Math.round(delta), unit);
        }
        delta /= amount;
    }
    return rtf.format(Math.round(delta), "years");
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

export function formatRelativeTime(dateStr: string | Date, now: Date = new Date()): string {
    return formatDistance(new Date(dateStr), now);
}

export function formatRelativeTimeWithAbsolute(
    dateStr: string | Date,
    now: Date = new Date(),
): string {
    const date = new Date(dateStr);
    const relative = formatDistance(date, now);
    const diffMs = Math.abs(now.getTime() - date.getTime());
    const diffDays = diffMs / (1000 * 60 * 60 * 24);

    let absolute: string;
    if (diffDays < 1) {
        absolute = date.toLocaleTimeString(undefined, {
            hour: "numeric",
            minute: "2-digit",
            hour12: false,
        });
    } else {
        absolute = date.toLocaleDateString(undefined, { month: "short", day: "numeric" });
    }

    return `${relative} (${absolute})`;
}

export function formatDateTime(dateStr: string): string {
    return new Date(dateStr).toLocaleString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
    });
}

/** Wall-clock time of day, seconds included, 24-hour — e.g. "17:15:02". */
export function formatClockTime(dateStr: string): string {
    return new Date(dateStr).toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
    });
}

/** Calendar date without the time — e.g. "22 Jun 2026". */
export function formatCalendarDate(dateStr: string): string {
    return new Date(dateStr).toLocaleDateString(undefined, {
        year: "numeric",
        month: "short",
        day: "numeric",
    });
}

/** Time of day, no seconds, 24-hour — e.g. "17:15". */
export function formatTimeHM(dateStr: string): string {
    return new Date(dateStr).toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
    });
}

/** Day and month, no year — e.g. "22 Jun". */
export function formatDayMonth(dateStr: string): string {
    return new Date(dateStr).toLocaleDateString(undefined, {
        month: "short",
        day: "numeric",
    });
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

export function formatFullDateTime(date: Date | string): string {
    return new Intl.DateTimeFormat(undefined, {
        year: "numeric",
        month: "short",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
    }).format(new Date(date));
}
