// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Task } from "@runwisp/common";

/**
 * Currently configured instance count for a task. Services report at least 1
 * (the default when `instances` is unset); everything else reports 1, so only
 * a multi-instance service ever renders a `#N` instance suffix.
 */
function taskInstanceCount(task: Pick<Task, "kind" | "instances">): number {
    if (task.kind === "service") {
        return Math.max(1, task.instances ?? 1);
    }
    return 1;
}

/**
 * Build a `taskName → instance count` resolver from a task list. Unknown names
 * default to 1 (no suffix), so runs whose task is missing from the list render
 * the bare name.
 */
export function instanceCountResolver(
    tasks: Pick<Task, "name" | "kind" | "instances">[],
): (taskName: string) => number {
    const byName = new Map<string, number>();
    for (const task of tasks) {
        byName.set(task.name, taskInstanceCount(task));
    }
    return (taskName) => byName.get(taskName) ?? 1;
}
