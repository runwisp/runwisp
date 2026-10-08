// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

interface RhythmInput {
    count: number;
    createdAt: Date | string;
    lastOccurredAt: Date | string;
    occurrences: (Date | string)[]; // newest first
    now?: Date;
}

const SECOND = 1000;
const MINUTE = 60 * SECOND;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;
const MONTH = 30 * DAY;
const YEAR = 365 * DAY;

/**
 * Returns a short human-readable phrase for the difference between `date` and
 * `now`. Mirrors `RelativeTime()` in `apps/runwisp/internal/tui/uikit/helpers.go`:
 * when changing thresholds here, update both implementations and
 * __rhythm_vectors.json.
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

/**
 * Order of rules matters; the first match wins. `notification-rhythm.test.ts`
 * pins the output against __rhythm_vectors.json.
 */
export function phrase(input: RhythmInput): string {
    const now = input.now ?? new Date();
    const last = new Date(input.lastOccurredAt);

    if (input.count <= 1) {
        return relative(last, now);
    }

    const occ = input.occurrences.map((v) => new Date(v));
    if (allWithin(occ, now, HOUR)) {
        return `${input.count.toString()}× in the last hour, latest ${relative(last, now)}`;
    }
    if (allWithin(occ, now, DAY)) {
        return `${input.count.toString()}× today, latest ${relative(last, now)}`;
    }

    const created = new Date(input.createdAt);
    const span = now.getTime() - created.getTime();
    if (span >= WEEK) {
        return `${input.count.toString()}× since ${relative(created, now)}, latest ${relative(last, now)}`;
    }
    return `${input.count.toString()}× over ${formatSpan(span)}, latest ${relative(last, now)}`;
}

const BLOCKS = "▁▂▃▄▅▆▇█";

/**
 * Buckets the occurrences by age relative to `now`, over the supplied window. Returns a
 * fixed-length string of unicode block characters; heavy on the right means a
 * recent burst.
 */
export function sparkline(
    occurrences: (Date | string)[],
    now: Date,
    windowMs: number,
    cells = 8,
): string {
    const cellCount = cells > 0 ? cells : 8;
    if (occurrences.length === 0 || windowMs <= 0) return "";
    const buckets = bucketOccurrences(occurrences, now, windowMs, cellCount);
    const max = Math.max(...buckets);
    if (max === 0) return "";
    return renderBlocks(buckets, max);
}

function bucketOccurrences(
    occurrences: (Date | string)[],
    now: Date,
    windowMs: number,
    cells: number,
): number[] {
    const buckets = new Array<number>(cells).fill(0);
    for (const raw of occurrences) {
        const age = Math.max(0, now.getTime() - new Date(raw).getTime());
        if (age >= windowMs) continue;
        const idx = clamp(cells - 1 - Math.floor((age / windowMs) * cells), 0, cells - 1);
        buckets[idx] = (buckets[idx] ?? 0) + 1;
    }
    return buckets;
}

function renderBlocks(buckets: number[], max: number): string {
    const blocks = BLOCKS.split("");
    const last = blocks.length - 1;
    const lowest = blocks[0] ?? "";
    return buckets
        .map((b) => {
            if (b === 0) return lowest;
            const idx = clamp(Math.round((b / max) * last), 0, last);
            return blocks[idx] ?? lowest;
        })
        .join("");
}

function clamp(n: number, lo: number, hi: number): number {
    if (n < lo) return lo;
    if (n > hi) return hi;
    return n;
}

function allWithin(occ: Date[], now: Date, windowMs: number): boolean {
    if (occ.length === 0) return false;
    const cutoff = now.getTime() - windowMs;
    for (const t of occ) {
        if (t.getTime() < cutoff) return false;
    }
    return true;
}

function formatSpan(ms: number): string {
    if (ms < HOUR) return `${Math.floor(ms / MINUTE).toString()}m`;
    if (ms < DAY) return `${Math.floor(ms / HOUR).toString()}h`;
    if (ms < WEEK) return `${Math.floor(ms / DAY).toString()}d`;
    if (ms < MONTH) {
        const weeks = Math.floor(ms / WEEK);
        return weeks <= 1 ? "1 week" : `${weeks.toString()} weeks`;
    }
    const months = Math.floor(ms / MONTH);
    return months <= 1 ? "1 month" : `${months.toString()} months`;
}
