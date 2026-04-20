// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createLogger } from "$lib/utils/logger";
import type { LogEvent } from "@runwisp/ui";
import { parseLogFetchResult, createLogStreamer } from "$lib/logs";
import { tasksApi } from "$lib/api";
import type { Run } from "$lib/types";

const logger = createLogger("LogSession");

interface LogSessionOptions {
    findRun: (runId: string) => Run | undefined;
    getTaskName: (run: Run) => string;
}

export interface LogSession {
    fetchLogs: (runId: string, from: number, to: number) => Promise<LogEvent>;
    streamLogs: (runId: string, onEvent: (event: LogEvent) => void) => () => void;
}

/**
 * Creates a reusable log session that provides fetchLogs/streamLogs callbacks
 * bound to a dynamic run lookup. Caches per-task streamers internally.
 */
export function createLogSession(options: LogSessionOptions): LogSession {
    const streamerCache = new Map<string, ReturnType<typeof createLogStreamer>>();

    function getStreamer(taskName: string) {
        let streamer = streamerCache.get(taskName);
        if (!streamer) {
            streamer = createLogStreamer(taskName);
            streamerCache.set(taskName, streamer);
        }
        return streamer;
    }

    async function fetchLogs(runId: string, from: number, to: number): Promise<LogEvent> {
        const run = options.findRun(runId);
        const finished = run ? run.status === "ended" : true;

        if (!run) {
            return { lines: {}, sizeLines: 0, sizeBytes: 0, finished };
        }

        try {
            const result = await tasksApi.getLog(options.getTaskName(run), runId, {
                start: from,
                end: to,
            });
            return parseLogFetchResult(result, from, finished);
        } catch (err) {
            logger.error("Failed to fetch logs", err);
            return { lines: {}, sizeLines: 0, sizeBytes: 0, finished };
        }
    }

    function streamLogs(runId: string, onEvent: (event: LogEvent) => void): () => void {
        const run = options.findRun(runId);
        if (!run) return () => {};
        return getStreamer(options.getTaskName(run))(runId, onEvent);
    }

    return { fetchLogs, streamLogs };
}
