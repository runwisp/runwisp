// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { relative } from "./format-time";

export interface RhythmInput {
    count: number;
    createdAt: Date | string;
    lastOccurredAt: Date | string;
    occurrences: (Date | string)[]; // newest first
    now?: Date;
}

const HOUR_MS = 60 * 60 * 1000;
const DAY_MS = 24 * HOUR_MS;
const WEEK_MS = 7 * DAY_MS;
const MONTH_MS = 30 * DAY_MS;

function toDate(v: Date | string): Date {
    return typeof v === "string" ? new Date(v) : v;
}

/**
 * Mirrors `Phrase()` in `internal/notify/render/rhythm.go`. Order of rules
 * matters; the first match wins. Keep the two implementations in sync — the
 * parity test in `notification-rhythm.test.ts` verifies vector-equivalence
 * with the Go side.
 */
export function phrase(input: RhythmInput): string {
    const now = input.now ?? new Date();
    const last = toDate(input.lastOccurredAt);

    if (input.count <= 1) {
        return relative(last, now);
    }

    const occ = input.occurrences.map(toDate);
    if (allWithin(occ, now, HOUR_MS)) {
        return `${input.count.toString()}× in the last hour, latest ${relative(last, now)}`;
    }
    if (allWithin(occ, now, DAY_MS)) {
        return `${input.count.toString()}× today, latest ${relative(last, now)}`;
    }

    const created = toDate(input.createdAt);
    const span = now.getTime() - created.getTime();
    if (span >= WEEK_MS) {
        return `${input.count.toString()}× since ${relative(created, now)}, latest ${relative(last, now)}`;
    }
    return `${input.count.toString()}× over ${formatSpan(span)}, latest ${relative(last, now)}`;
}

const BLOCKS = "▁▂▃▄▅▆▇█";

/**
 * Mirrors `Sparkline()` in `internal/notify/render/rhythm.go`. Buckets the
 * occurrences by age relative to `now`, over the supplied window. Returns a
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
        const age = Math.max(0, now.getTime() - toDate(raw).getTime());
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
    if (ms < 60 * 60 * 1000) return `${Math.floor(ms / (60 * 1000)).toString()}m`;
    if (ms < 24 * 60 * 60 * 1000) return `${Math.floor(ms / (60 * 60 * 1000)).toString()}h`;
    if (ms < 7 * 24 * 60 * 60 * 1000)
        return `${Math.floor(ms / (24 * 60 * 60 * 1000)).toString()}d`;
    if (ms < MONTH_MS) {
        const weeks = Math.floor(ms / WEEK_MS);
        return weeks <= 1 ? "1 week" : `${weeks.toString()} weeks`;
    }
    const months = Math.floor(ms / MONTH_MS);
    return months <= 1 ? "1 month" : `${months.toString()} months`;
}
