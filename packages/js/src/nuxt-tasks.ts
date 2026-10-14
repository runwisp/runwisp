// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Nitro plugin added by the Nuxt module when the app has `scheduledTasks`:
// each scheduled Nitro task becomes a RunWisp task with the same schedule,
// which runs it with runTask().

import { runTask } from "#imports";
import { options } from "#runwisp/nuxt";
import { scheduled } from "#runwisp/nuxt-internal";

import { RunWisp } from "@runwisp/js";

export default function (): void {
    scheduleNitroTasks(import.meta.prerender);
}

export function scheduleNitroTasks(prerender: boolean): void {
    // `nuxi build` runs the plugins to prerender routes: registering the
    // tasks there would start RunWisp in the build.
    if (prerender) return;
    const runwisp = new RunWisp(options);
    const seen = new Map<string, number>();
    for (const [cron, task] of scheduled) {
        const count = (seen.get(task) ?? 0) + 1;
        seen.set(task, count);
        const name = count === 1 ? task : `${task}-${String(count)}`;
        // As under Nitro's scheduler: a missed time is not run late, and
        // runTask() joins a run of the task already going instead of
        // starting another.
        runwisp.task(name, { cron, catch_up: 0, on_overlap: "skip", max_concurrent: 1 }, () =>
            runTask(task, { payload: {} }),
        );
    }
}
