// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { LogEvent } from "@runwisp/ui";
import { browser } from "$app/environment";
import { browserAuthEventSourceFactory } from "$lib/adapters/browser";
import { getEventSourceErrorDetails, getMessageEventData } from "$lib/utils/event-source";
import { buildSSEUrl } from "$lib/utils/sse";
import { getApiUrl } from "$lib/utils/env";
import { SSE_CONFIG } from "$lib/config/constants";
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
        let disposed = false;
        let source: EventSource | null = null;
        let reconnectTimeout: ReturnType<typeof setTimeout> | null = null;
        let reconnectDelay: number = SSE_CONFIG.RECONNECT_DELAY;

        function connect() {
            if (disposed || finished) return;

            // On reconnect, discard any partial line from the previous connection.
            // The server will resume from byteOffset, so line numbering stays valid.
            buffer = "";

            const base = `/api/tasks/${taskName}/runs/${runId}/log-stream`;
            const url = buildSSEUrl(
                byteOffset > 0 ? base + "?offset=" + String(byteOffset) : base,
                getApiUrl(),
            );
            logger.info(
                "Connecting to log stream: " +
                    url +
                    ", byteOffset=" +
                    String(byteOffset) +
                    ", lineCount=" +
                    String(lineCount),
            );
            source = browserAuthEventSourceFactory(url);

            source.onopen = () => {
                logger.info(`Log stream connection opened: ${taskName}/${runId}`);
                reconnectDelay = SSE_CONFIG.RECONNECT_DELAY;
            };

            source.onmessage = (e) => {
                try {
                    const rawChunk = getMessageEventData(e);
                    if (rawChunk === undefined) {
                        throw new Error("log stream chunk was not a string");
                    }
                    const chunk = parseStreamChunk(rawChunk);
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
            };

            source.addEventListener("done", () => {
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
                cleanup();
            });

            source.addEventListener("metadata", (e) => {
                const data = getMessageEventData(e);
                logger.info("Log stream metadata: " + taskName + "/" + runId, data);
            });

            source.onerror = (e) => {
                const { status, message } = getEventSourceErrorDetails(e);
                logger.warn(
                    "Log stream error for " + taskName + "/" + runId + ":",
                    (message ?? "connection lost") +
                        (status !== undefined ? " (HTTP " + String(status) + ")" : ""),
                );
                cleanup();
                scheduleReconnect();
            };
        }

        function cleanup() {
            if (source) {
                source.close();
                source = null;
            }
        }

        function scheduleReconnect() {
            if (disposed || finished) return;
            const delay = reconnectDelay;
            reconnectDelay = Math.min(reconnectDelay * 2, SSE_CONFIG.MAX_RECONNECT_DELAY);
            logger.debug("Reconnecting log stream in " + String(delay) + "ms");
            reconnectTimeout = setTimeout(connect, delay);
        }

        connect();

        return () => {
            disposed = true;
            if (reconnectTimeout) {
                clearTimeout(reconnectTimeout);
                reconnectTimeout = null;
            }
            cleanup();
        };
    };
}
