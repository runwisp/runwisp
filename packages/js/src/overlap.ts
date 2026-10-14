// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { TaskConfigOptions } from "./runwisp.ts";

/**
 * Lets a task's runs overlap, as the timers and cron jobs the integrations
 * replace do. RunWisp always caps concurrent runs, so the cap is high, as in
 * schedule().
 */
export const OVERLAPPING: Pick<TaskConfigOptions, "on_overlap" | "max_concurrent"> = {
    on_overlap: "queue",
    max_concurrent: 1024,
};
