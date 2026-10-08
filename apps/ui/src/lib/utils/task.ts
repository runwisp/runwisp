// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Component } from "svelte";
import { AppWindow, CalendarClock, CalendarOff, CircleDot } from "@lucide/svelte";
import { isService, type Task } from "@runwisp/common";

/** Whether the task has a cron schedule. */
export function hasCron(task: Pick<Task, "cron">): boolean {
    return Boolean(task.cron?.trim());
}

/**
 * Currently configured instance count for a task. Services report at least 1
 * (the default when `instances` is unset); everything else reports 1, so only
 * a multi-instance service ever renders a `#N` instance suffix.
 */
export function taskInstanceCount(task: Pick<Task, "kind" | "instances">): number {
    return isService(task.kind) ? Math.max(1, task.instances ?? 1) : 1;
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

/** "Service" or "Service ×N" for a multi-instance service. */
export function serviceLabel(task: Pick<Task, "kind" | "instances">): string {
    const instances = taskInstanceCount(task);
    return instances > 1 ? `Service ×${String(instances)}` : "Service";
}

/**
 * Whether a service is stopped (by an operator, or not autostarted), so its
 * control offers Start instead of Restart. Read from the daemon's task data,
 * not page state, so it survives a reload and other tabs.
 */
export function isServiceStopped(task: Pick<Task, "kind" | "serviceStopped">): boolean {
    return isService(task.kind) && (task.serviceStopped ?? false);
}

/** Whether the top bar shows a schedule chip for the task: a cron task on a
 * daemon that schedules locally (in station mode the station owns it). */
export function showScheduleChip(task: Task, schedulingActive: boolean): boolean {
    return schedulingActive && !isService(task.kind) && hasCron(task);
}

/** Whether the operator may pause or resume the task's schedule. Mirrors the
 * daemon: manual_trigger must be on, and a task a system cron daemon still
 * holds can't be paused (but one paused before it was held can be resumed). */
export function canTogglePause(task: Task): boolean {
    return task.manualTrigger && (Boolean(task.pausedAt) || !task.heldBy);
}

export function taskIcon(task: Task): Component {
    if (isService(task.kind)) return AppWindow;
    if (hasCron(task)) return task.pausedAt ? CalendarOff : CalendarClock;
    return CircleDot;
}

export function taskTriggerTooltip(task: Task): string {
    if (isService(task.kind)) return serviceLabel(task);
    if (task.cron && hasCron(task)) {
        return task.pausedAt ? `Cron · ${task.cron} · paused` : `Cron · ${task.cron}`;
    }
    return "Manual trigger";
}
