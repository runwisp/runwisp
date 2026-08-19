// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LogEvent } from "@runwisp/ui";
import { browser } from "$app/environment";
import { z } from "zod";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { connectSSE } from "$lib/utils/sse";
import { getApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

const logger = createLogger("LogStreamer");

const logPageLineSchema = z.object({
    n: z.number().int(),
    ts: z.number().int().optional(),
    stream: z.string(),
    text: z.string(),
    continued: z.boolean().optional(),
    frameCount: z.number().int().nonnegative().optional(),
});

export const logLineHistorySchema = z.object({
    frames: z.array(z.array(z.string())),
});

export type LogLineHistory = z.infer<typeof logLineHistorySchema>;

export const logPageSchema = z.object({
    lines: z.array(logPageLineSchema),
    firstAvailable: z.number().int().nonnegative(),
    totalLines: z.number().int().nonnegative(),
    truncated: z.boolean(),
    finalized: z.boolean(),
});

const regionSchema = z.object({
    stream: z.string(),
    epoch: z.number().int(),
    rows: z.array(z.string()),
});

const rotatedSchema = z.object({ firstAvailable: z.number().int().nonnegative() });
const droppedSchema = z.object({
    after: z.number().int(),
    count: z.number().int().nonnegative(),
});
const doneSchema = z.object({
    finalLine: z.number().int(),
    status: z.string(),
});

export type LogPageLine = z.infer<typeof logPageLineSchema>;
export type LogPage = z.infer<typeof logPageSchema>;

const logSearchHitSchema = z.object({
    runId: z.string(),
    n: z.number().int().nonnegative(),
    stream: z.string(),
    text: z.string(),
    ts: z.number().int(),
});

export const logSearchResponseSchema = z.object({
    items: z.array(logSearchHitSchema),
    nextCursor: z.string().optional().default(""),
    exhausted: z.boolean(),
    scannedRuns: z.number().int().nonnegative(),
});

export type LogSearchHit = z.infer<typeof logSearchHitSchema>;
export type LogSearchResponse = z.infer<typeof logSearchResponseSchema>;

/** Convert a daemon LogPage into the LogEvent shape consumed by LogConsole. */
export function parseLogPage(page: LogPage): LogEvent {
    const slice: Record<number, string> = {};
    const frameCounts: Record<number, number> = {};
    for (const l of page.lines) {
        slice[l.n] = l.text;
        if (l.frameCount !== undefined && l.frameCount > 0) frameCounts[l.n] = l.frameCount;
    }
    const out: LogEvent = {
        lines: slice,
        sizeLines: page.totalLines,
        finished: page.finalized,
    };
    if (page.firstAvailable > 0) {
        out.firstAvailableLine = page.firstAvailable;
    }
    if (Object.keys(frameCounts).length > 0) out.frameCounts = frameCounts;
    return out;
}

export interface LogStreamInitialState {
    fromLine: number;
}

interface StreamerState {
    totalLines: number;
    firstAvailable: number;
    lastReceivedId: number;
    finished: boolean;
}

function buildLineEvent(state: StreamerState, line: LogPageLine): LogEvent {
    const evt: LogEvent = {
        lines: { [line.n]: line.text },
        sizeLines: state.totalLines,
        finished: false,
    };
    if (state.firstAvailable > 0) evt.firstAvailableLine = state.firstAvailable;
    if (line.frameCount !== undefined && line.frameCount > 0) {
        evt.frameCounts = { [line.n]: line.frameCount };
    }
    return evt;
}

function handleLineEvent(state: StreamerState, data: string, onEvent: (event: LogEvent) => void) {
    const result = logPageLineSchema.safeParse(JSON.parse(data));
    if (!result.success) return;
    const line = result.data;
    state.lastReceivedId = line.n;
    if (line.n + 1 > state.totalLines) state.totalLines = line.n + 1;
    onEvent(buildLineEvent(state, line));
}

function handleRegionEvent(state: StreamerState, data: string, onEvent: (event: LogEvent) => void) {
    const result = regionSchema.safeParse(JSON.parse(data));
    if (!result.success) return;
    const region = result.data;
    // Region snapshots carry no line number and are never persisted; they only
    // update the transient in-place overlay. Don't advance lastReceivedId.
    onEvent({
        lines: {},
        sizeLines: state.totalLines,
        finished: false,
        region: { stream: region.stream, epoch: region.epoch, rows: region.rows },
    });
}

function handleRotatedEvent(
    state: StreamerState,
    data: string,
    onEvent: (event: LogEvent) => void,
) {
    const result = rotatedSchema.safeParse(JSON.parse(data));
    if (!result.success) return;
    if (result.data.firstAvailable > state.firstAvailable) {
        state.firstAvailable = result.data.firstAvailable;
        onEvent({
            lines: {},
            sizeLines: state.totalLines,
            finished: false,
            firstAvailableLine: state.firstAvailable,
        });
    }
}

function handleDroppedEvent(data: string) {
    const result = droppedSchema.safeParse(JSON.parse(data));
    if (!result.success) return;
    logger.warn(
        `Log stream dropped ${String(result.data.count)} line(s) after #${String(
            result.data.after,
        )}`,
    );
}

function handleDoneEvent(
    state: StreamerState,
    data: string,
    onEvent: (event: LogEvent) => void,
): boolean {
    const result = doneSchema.safeParse(JSON.parse(data));
    if (!result.success) return false;
    state.finished = true;
    const sz = Math.max(result.data.finalLine + 1, state.totalLines);
    const evt: LogEvent = { lines: {}, sizeLines: sz, finished: true };
    if (state.firstAvailable > 0) evt.firstAvailableLine = state.firstAvailable;
    onEvent(evt);
    return true;
}

export function createLogStreamer(taskName: string) {
    return (
        runId: string,
        onEvent: (event: LogEvent) => void,
        initialState?: LogStreamInitialState,
    ): (() => void) => {
        if (!browser) return () => {};

        const state: StreamerState = {
            totalLines: 0,
            firstAvailable: 0,
            lastReceivedId: -1,
            finished: false,
        };

        const startFrom = initialState?.fromLine ?? -1000;
        const base = `/api/runs/${runId}/log/stream`;
        const connection = connectSSE({
            path: () => {
                const from = state.lastReceivedId >= 0 ? state.lastReceivedId + 1 : startFrom;
                return base + "?from=" + String(from);
            },
            eventTypes: ["line", "region", "rotated", "dropped", "done"],
            onOpen: () => {
                logger.info(`Log stream connection opened: ${taskName}/${runId}`);
            },
            onError: (info) => {
                if (state.finished) return;
                logger.warn(
                    "Log stream error for " + taskName + "/" + runId + ":",
                    (info.message ?? "connection lost") +
                        (info.status === undefined ? "" : " (HTTP " + String(info.status) + ")"),
                );
            },
            onEvent: (eventType, data) => {
                try {
                    if (eventType === "line") {
                        handleLineEvent(state, data, onEvent);
                    } else if (eventType === "region") {
                        handleRegionEvent(state, data, onEvent);
                    } else if (eventType === "rotated") {
                        handleRotatedEvent(state, data, onEvent);
                    } else if (eventType === "dropped") {
                        handleDroppedEvent(data);
                    } else if (eventType === "done" && handleDoneEvent(state, data, onEvent)) {
                        connection.disconnect();
                    }
                } catch (err) {
                    logger.error(`Failed to parse SSE ${eventType} payload`, err);
                }
            },
            deps: {
                createEventSource: browserAuthEventSourceFactory,
                getApiUrl,
            },
        });

        return () => {
            state.finished = true;
            connection.disconnect();
        };
    };
}
