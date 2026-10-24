// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Component } from "svelte";
import { AppWindow, CalendarClock, CalendarOff, CircleDot, PowerOff } from "@lucide/svelte";
import { isService, type Task } from "@runwisp/common";
import { formatRelativeTimeWithAbsolute } from "@runwisp/ui";

/** Why a held task produces no runs, and how to hand it over. */
export const HELD_BY_CRON_HELP =
    "Held: a system cron daemon still owns this job, so RunWisp is not " +
    "running it; cron is. RunWisp records no history or output for it " +
    "until cron is retired. Run `sudo runwisp takeover` to hand it over, " +
    "or stop cron and disable it at boot (for example " +
    "`sudo systemctl disable --now cron`): RunWisp picks it up on its own " +
    "within a minute.";

/**
 * The zone a task's cron is evaluated in: its own timezone, else the daemon's.
 * Next-run times are shown in it so they agree with the cron description.
 */
export function scheduleTimeZone(
    task: Pick<Task, "timezone">,
    daemonZone: string,
): string | undefined {
    if (task.timezone) return task.timezone;
    return daemonZone ? daemonZone : undefined;
}

/** Whether the task has a cron schedule. */
export function hasCron(task: Pick<Task, "cron">): task is Pick<Task, "cron"> & { cron: string } {
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

/** Whether the operator may pause or resume the task's schedule. Mirrors the
 * daemon: manual_trigger must be on, and a task a system cron daemon still
 * holds can't be paused (but one paused before it was held can be resumed). */
export function canTogglePause(task: Task): boolean {
    return task.manualTrigger && (Boolean(task.pausedAt) || !task.heldBy);
}

export function taskIcon(task: Task): Component {
    if (isService(task.kind)) return isServiceStopped(task) ? PowerOff : AppWindow;
    if (hasCron(task)) return task.pausedAt ? CalendarOff : CalendarClock;
    return CircleDot;
}

export function taskTriggerTooltip(task: Task): string {
    if (isService(task.kind)) {
        return isServiceStopped(task) ? `${serviceLabel(task)} · stopped` : serviceLabel(task);
    }
    if (hasCron(task)) {
        return task.pausedAt ? `Cron · ${task.cron} · paused` : `Cron · ${task.cron}`;
    }
    return "Manual trigger";
}

/**
 * The task page's empty state: why a task has no runs yet. No Run button
 * beside it, the task strip's is right above. `now` ticks, so "the first run
 * is in 5 minutes" counts down.
 */
export function noRunsMessage(
    task: Task,
    schedulingActive: boolean,
    now: Date,
    daemonZone = "",
): { title: string; description: string } {
    if (task.heldBy) {
        return {
            title: "No runs recorded",
            description:
                "While cron holds this job, it runs outside RunWisp. Hand it over to see its runs here.",
        };
    }
    if (hasCron(task) && task.nextRunAt && schedulingActive) {
        return {
            title: "No runs yet",
            description: `The first run is ${formatRelativeTimeWithAbsolute(task.nextRunAt, now, scheduleTimeZone(task, daemonZone))}.`,
        };
    }
    if (isService(task.kind)) {
        return {
            title: "No runs yet",
            description: "Each instance's output shows here once it starts.",
        };
    }
    return {
        title: "No runs yet",
        description: `Use Run above, runwisp run ${task.name}, or the REST API.`,
    };
}
