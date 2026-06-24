// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type LogSlice = Record<number, string>;

/**
 * RegionUpdate is a live snapshot of a still-animating output region (a `\r`
 * progress bar or a multi-line ANSI redraw). It is never persisted: the cache
 * holds it in a transient overlay keyed by stream, replacing the whole frame on
 * each update. Empty `rows` clears the overlay for that stream.
 */
export type RegionUpdate = {
    stream: string;
    epoch: number;
    rows: string[];
};

export type LogEvent = {
    lines: LogSlice;
    sizeLines: number;
    sizeBytes?: number;
    finished: boolean;
    firstAvailableLine?: number;
    region?: RegionUpdate;
    // Per-line count of recorded prior frames, keyed by line number. Present only
    // for settled progress-bar / multi-line-redraw anchor lines, marking them as
    // clickable to rewind through earlier states. Plain lines are absent.
    frameCounts?: Record<number, number>;
};

export function isLogEvent(value: unknown): value is LogEvent {
    return value !== null && typeof value === "object" && "sizeLines" in value;
}

export type FetchLogsFn = (
    from: number,
    to: number,
) => Promise<LogSlice | LogEvent | undefined> | LogSlice | LogEvent | undefined;
