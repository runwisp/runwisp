// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const MONTH = 30 * DAY;
const YEAR = 365 * DAY;

/**
 * Returns a short human-readable phrase for the difference between `date` and
 * `now`. Mirrors the Go-side `relative()` in `internal/notify/render/rhythm.go`
 * — when changing thresholds here, update both implementations and the
 * exported parity vectors in __rhythm_vectors.json.
 */
export function relative(date: Date | string, now: Date = new Date()): string {
    const t = typeof date === "string" ? new Date(date) : date;
    if (Number.isNaN(t.getTime())) return "";

    const d = now.getTime() - t.getTime();
    if (d < 30 * SECOND) return "just now";
    if (d < MINUTE) return `${Math.floor(d / SECOND).toString()}s ago`;
    if (d < HOUR) return `${Math.floor(d / MINUTE).toString()}m ago`;
    if (d < DAY) return `${Math.floor(d / HOUR).toString()}h ago`;
    if (d < 2 * DAY) return "yesterday";
    if (d < MONTH) return `${Math.floor(d / DAY).toString()}d ago`;
    if (d < YEAR) {
        const months = Math.floor(d / MONTH);
        return months <= 1 ? "1mo ago" : `${months.toString()}mo ago`;
    }
    const years = Math.floor(d / YEAR);
    return years <= 1 ? "1y ago" : `${years.toString()}y ago`;
}
