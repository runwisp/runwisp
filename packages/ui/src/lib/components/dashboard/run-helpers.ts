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
