// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { createLogger } from "$lib/utils/logger";
import type { LogEvent } from "@runwisp/ui";
import { parseLogPage, createLogStreamer, type LogStreamInitialState } from "$lib/logs";
import { tasksApi } from "$lib/api";
import type { Run } from "$lib/types";

const logger = createLogger("LogSession");

interface LogSessionOptions {
    findRun: (runId: string) => Run | undefined;
    getTaskName: (run: Run) => string;
}

/**
 * Creates a reusable log session that provides fetchLogs/streamLogs callbacks
 * bound to a dynamic run lookup. Caches per-task streamers internally.
 */
export function createLogSession(options: LogSessionOptions) {
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
            return { lines: {}, sizeLines: 0, finished };
        }

        const limit = Math.max(1, to - from + 1);
        try {
            const page = await tasksApi.getLogPage(runId, {
                from,
                limit,
            });
            return parseLogPage(page);
        } catch (err) {
            // Log for diagnostics, then rethrow instead of swallowing into a
            // benign empty LogEvent: callers (RunDetailPanel's seed fetch,
            // LogConsole's internal LogFetcher) already catch this and must be
            // able to tell "fetch failed" apart from "run produced no output".
            logger.error("Failed to fetch logs", err);
            throw err;
        }
    }

    function streamLogs(
        runId: string,
        onEvent: (event: LogEvent) => void,
        initialState?: LogStreamInitialState,
    ): () => void {
        const run = options.findRun(runId);
        if (!run) return () => {};
        return getStreamer(options.getTaskName(run))(runId, onEvent, initialState);
    }

    async function fetchLineHistory(runId: string, lineNum: number): Promise<string[][]> {
        const run = options.findRun(runId);
        if (!run) return [];
        try {
            return await tasksApi.getLogLineHistory(runId, lineNum);
        } catch (err) {
            // LogConsole's frame-history toggle already catches this and
            // renders "Failed to load frame history" — swallowing it here
            // instead hid every failure behind a silent no-op.
            logger.error("Failed to fetch log line history", err);
            throw err;
        }
    }

    return { fetchLogs, streamLogs, fetchLineHistory };
}
