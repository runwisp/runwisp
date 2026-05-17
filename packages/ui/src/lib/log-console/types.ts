// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

export type LogSlice = Record<number, string>;

export type LogEvent = {
    lines: LogSlice;
    sizeLines: number;
    sizeBytes?: number;
    finished: boolean;
    firstAvailableLine?: number;
};

export function isLogEvent(value: unknown): value is LogEvent {
    return value !== null && typeof value === "object" && "sizeLines" in value;
}

export type FetchLogsFn = (
    from: number,
    to: number,
) => Promise<LogSlice | LogEvent | undefined> | LogSlice | LogEvent | undefined;
