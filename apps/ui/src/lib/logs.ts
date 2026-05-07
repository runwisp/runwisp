// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
});

export const logPageSchema = z.object({
    lines: z.array(logPageLineSchema),
    first_available: z.number().int().nonnegative(),
    total_lines: z.number().int().nonnegative(),
    truncated: z.boolean(),
    finalized: z.boolean(),
});

const rotatedSchema = z.object({ first_available: z.number().int().nonnegative() });
const droppedSchema = z.object({
    after: z.number().int(),
    count: z.number().int().nonnegative(),
});
const doneSchema = z.object({
    final_line: z.number().int(),
    status: z.string(),
});

export type LogPageLine = z.infer<typeof logPageLineSchema>;
export type LogPage = z.infer<typeof logPageSchema>;

/** Convert a daemon LogPage into the LogEvent shape consumed by LogConsole. */
export function parseLogPage(page: LogPage): LogEvent {
    const slice: Record<number, string> = {};
    for (const l of page.lines) {
        slice[l.n] = l.text;
    }
    const out: LogEvent = {
        lines: slice,
        sizeLines: page.total_lines,
        finished: page.finalized,
    };
    if (page.first_available > 0) {
        out.firstAvailableLine = page.first_available;
    }
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

function handleRotatedEvent(
    state: StreamerState,
    data: string,
    onEvent: (event: LogEvent) => void,
) {
    const result = rotatedSchema.safeParse(JSON.parse(data));
    if (!result.success) return;
    if (result.data.first_available > state.firstAvailable) {
        state.firstAvailable = result.data.first_available;
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
    const sz =
        result.data.final_line + 1 > state.totalLines
            ? result.data.final_line + 1
            : state.totalLines;
    const evt: LogEvent = { lines: {}, sizeLines: sz, finished: true };
    if (state.firstAvailable > 0) evt.firstAvailableLine = state.firstAvailable;
    onEvent(evt);
    return true;
}

/** Create an SSE log streamer for a specific task. */
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
        const base = `/api/tasks/${taskName}/runs/${runId}/log/stream`;
        const connection = connectSSE({
            path: () => {
                const from = state.lastReceivedId >= 0 ? state.lastReceivedId + 1 : startFrom;
                return base + "?from=" + String(from);
            },
            eventTypes: ["line", "rotated", "dropped", "done"],
            onOpen: () => {
                logger.info(`Log stream connection opened: ${taskName}/${runId}`);
            },
            onError: (info) => {
                if (state.finished) return;
                logger.warn(
                    "Log stream error for " + taskName + "/" + runId + ":",
                    (info.message ?? "connection lost") +
                        (info.status !== undefined ? " (HTTP " + String(info.status) + ")" : ""),
                );
            },
            onEvent: (eventType, data) => {
                try {
                    if (eventType === "line") {
                        handleLineEvent(state, data, onEvent);
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
