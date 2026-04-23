// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { LogEvent } from "@runwisp/ui";
import { browser } from "$app/environment";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { connectSSE } from "$lib/utils/sse";
import { getApiUrl } from "$lib/utils/env";
import { createLogger } from "$lib/utils/logger";

const logger = createLogger("LogStreamer");

type LogFetchResult = { content: string; totalLines?: number; firstAvailableLine?: number };

function parseStreamChunk(raw: string): string | null {
    const parsed: unknown = JSON.parse(raw);
    return typeof parsed === "string" ? parsed : null;
}

/** Parse a raw log fetch result into a LogEvent. */
export function parseLogFetchResult(
    result: LogFetchResult,
    from: number,
    finished: boolean,
): LogEvent {
    const lines = result.content.split(/\r?\n/);
    if (result.content.endsWith("\n") && lines.length > 0) {
        lines.pop();
    }

    // When the server clamped start_line to firstAvailableLine
    // (because earlier lines were rotated away), the returned
    // content starts at that line, not at `from`.
    const actualFrom =
        result.firstAvailableLine !== undefined && result.firstAvailableLine > from
            ? result.firstAvailableLine
            : from;

    const slice: Record<number, string> = {};
    lines.forEach((line, i) => {
        slice[actualFrom + i] = line;
    });

    const out: LogEvent = {
        lines: slice,
        sizeLines: result.totalLines ?? actualFrom + lines.length,
        sizeBytes: result.content.length,
        finished,
    };
    if (result.firstAvailableLine !== undefined) {
        out.firstAvailableLine = result.firstAvailableLine;
    }
    return out;
}

/** Create an SSE log streamer for a specific task. Returns a function that connects to a run's log stream. */
export function createLogStreamer(taskName: string) {
    return (runId: string, onEvent: (event: LogEvent) => void): (() => void) => {
        if (!browser) return () => {};

        let lineCount = 0;
        let buffer = "";
        let byteOffset = 0;
        let finished = false;

        const base = `/api/tasks/${taskName}/runs/${runId}/log-stream`;
        const connection = connectSSE({
            path: () => (byteOffset > 0 ? base + "?offset=" + String(byteOffset) : base),
            eventTypes: ["message", "done", "metadata"],
            onOpen: () => {
                logger.info(`Log stream connection opened: ${taskName}/${runId}`);
                // On reconnect, discard any partial line from the previous connection.
                // The server resumes from byteOffset, so line numbering stays valid.
                buffer = "";
            },
            onError: (info) => {
                if (finished) return;
                logger.warn(
                    "Log stream error for " + taskName + "/" + runId + ":",
                    (info.message ?? "connection lost") +
                        (info.status !== undefined ? " (HTTP " + String(info.status) + ")" : ""),
                );
            },
            onEvent: (eventType, data) => {
                if (eventType === "message") {
                    try {
                        const chunk = parseStreamChunk(data);
                        if (chunk === null) {
                            throw new Error("log stream chunk was not a string");
                        }
                        byteOffset += new TextEncoder().encode(chunk).byteLength;
                        buffer += chunk;
                        const parts = buffer.split(/\r?\n/);

                        const completeLines = parts.slice(0, -1);
                        buffer = parts[parts.length - 1] ?? "";

                        if (completeLines.length > 0) {
                            const linesMap: Record<number, string> = {};
                            completeLines.forEach((text, i) => {
                                linesMap[lineCount + i] = text;
                            });
                            lineCount += completeLines.length;

                            onEvent({
                                lines: linesMap,
                                sizeLines: lineCount,
                                finished: false,
                            });
                        }
                    } catch (err) {
                        logger.error("Error parsing log stream chunk:", err);
                    }
                    return;
                }
                if (eventType === "done") {
                    logger.info(
                        'Log stream "done" event received: ' +
                            taskName +
                            "/" +
                            runId +
                            ", lineCount=" +
                            String(lineCount) +
                            ", bufferLen=" +
                            String(buffer.length),
                    );
                    finished = true;
                    if (buffer) {
                        onEvent({
                            lines: { [lineCount]: buffer },
                            sizeLines: lineCount + 1,
                            finished: true,
                        });
                    } else {
                        onEvent({
                            lines: {},
                            sizeLines: lineCount,
                            finished: true,
                        });
                    }
                    connection.disconnect();
                    return;
                }
                if (eventType === "metadata") {
                    logger.info("Log stream metadata: " + taskName + "/" + runId, data);
                }
            },
            deps: {
                createEventSource: browserAuthEventSourceFactory,
                getApiUrl,
            },
        });

        return () => {
            finished = true;
            connection.disconnect();
        };
    };
}
