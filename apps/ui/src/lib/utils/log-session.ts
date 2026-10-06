// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { LogEvent } from "@runwisp/ui";
import { parseLogPage, streamRunLog, type LogStreamInitialState } from "$lib/logs";
import { tasksApi } from "$lib/api";
import type { Run } from "@runwisp/common";

/**
 * fetchLogs/streamLogs/fetchLineHistory callbacks bound to a dynamic run
 * lookup. Fetch failures propagate: callers (RunDetailPanel's seed fetch,
 * LogConsole) must tell "fetch failed" apart from "run produced no output".
 */
export function createLogSession(findRun: (runId: string) => Run | undefined) {
    async function fetchLogs(runId: string, from: number, to: number): Promise<LogEvent> {
        const run = findRun(runId);
        if (!run) {
            return { lines: {}, sizeLines: 0, finished: true };
        }
        const limit = Math.max(1, to - from + 1);
        return parseLogPage(await tasksApi.getLogPage(runId, { from, limit }));
    }

    function streamLogs(
        runId: string,
        onEvent: (event: LogEvent) => void,
        initialState?: LogStreamInitialState,
    ): () => void {
        if (!findRun(runId)) return () => {};
        return streamRunLog(runId, onEvent, initialState);
    }

    async function fetchLineHistory(runId: string, lineNum: number): Promise<string[][]> {
        if (!findRun(runId)) return [];
        return tasksApi.getLogLineHistory(runId, lineNum);
    }

    return { fetchLogs, streamLogs, fetchLineHistory };
}
