// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { isService, type Task } from "@runwisp/common";

/**
 * Whether a service is stopped (by an operator, or not autostarted), so its
 * control offers Start instead of Restart. Read from the daemon's task data,
 * not page state, so it survives a reload and other tabs.
 */
export function isServiceStopped(task: Pick<Task, "kind" | "serviceStopped">): boolean {
    return isService(task.kind) && (task.serviceStopped ?? false);
}
