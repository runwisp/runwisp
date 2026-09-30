// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { isService, type Task } from "@runwisp/common";

/** Whether the top bar shows a schedule chip for the task: a cron task on a
 * daemon that schedules locally (in station mode the station owns it). */
export function showScheduleChip(task: Task, schedulingActive: boolean): boolean {
    return schedulingActive && !isService(task.kind) && Boolean(task.cron?.trim());
}

/** Whether the operator may pause or resume the task's schedule. Mirrors the
 * daemon: manual_trigger must be on, and a task a system cron daemon still
 * holds can't be paused (but one paused before it was held can be resumed). */
export function canTogglePause(task: Task): boolean {
    return task.manualTrigger && (Boolean(task.pausedAt) || !task.heldBy);
}
