// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { Run } from "@runwisp/common";
import { formatDuration } from "../../utils/format.js";

export function runDuration(run: Pick<Run, "start_at" | "end_at">): string | undefined {
    if (!run.start_at) return undefined;
    const start = new Date(run.start_at).getTime();
    const end = run.end_at ? new Date(run.end_at).getTime() : Date.now();
    return formatDuration(end - start);
}

/**
 * Monotonic position of a run's status in its lifecycle. Used to reject stale
 * updates (e.g. a `pending` HTTP response arriving after an SSE already
 * advanced the row to `success`). Higher = further along.
 */
export function runPhaseOrder(status: string): number {
    if (status === "pending") return 0;
    if (status === "running") return 1;
    return 2;
}
