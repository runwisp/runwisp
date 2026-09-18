// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import {
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    humanizeCron,
    humanizeStatus,
    runDuration,
} from "@runwisp/ui";
import type { TaskOverview } from "./overview.js";
import type { Run, Task } from "@runwisp/common";

// formatTriggeredByLabel now lives in the shared @runwisp/ui lib (used by the
// run list/detail components there too). Re-export so existing apps/ui imports
// and tests keep resolving it from this module.
export { formatTriggeredByLabel } from "@runwisp/ui";

export function pluralize(count: number): string {
    return count === 1 ? "" : "s";
}

// Compact a run count so the stat card never grows wide: 55653 → "55.6k",
// 123456 → "123k", 1430092 → "1.4m". One decimal below 100 of a unit, none at
// or above it; truncated (not rounded) so the shown figure never overstates.
export function formatCompactCount(count: number): string {
    const units = [
        { limit: 1e9, suffix: "b" },
        { limit: 1e6, suffix: "m" },
        { limit: 1e3, suffix: "k" },
    ];
    for (const { limit, suffix } of units) {
        if (count >= limit) {
            const scaled = count / limit;
            const factor = scaled >= 100 ? 1 : 10;
            return `${String(Math.floor(scaled * factor) / factor)}${suffix}`;
        }
    }
    return String(count);
}

export function formatTaskDescription(task: Task): string {
    return task.description ?? "No description yet. Open the task to review its execution details.";
}

export function formatTaskLastRunLabel(task: TaskOverview, now: Date = new Date()): string {
    if (!task.lastRun) {
        return "No runs yet";
    }

    return formatRelativeTimeWithAbsolute(task.lastRun.startedAt ?? task.lastRun.createdAt, now);
}

export function formatTaskLastResultLabel(task: TaskOverview): string {
    if (!task.lastStatus) {
        return "No runs yet";
    }

    return humanizeStatus(task.lastStatus);
}

export function formatTaskNextRunLabel(task: TaskOverview, now: Date = new Date()): string {
    if (task.task.kind === "service") {
        return "Always on";
    }

    if (task.nextRunMs !== undefined) {
        return formatRelativeTimeWithAbsolute(new Date(task.nextRunMs), now);
    }

    return task.isApiOnly ? "Manual only" : "Not scheduled";
}

export function formatTaskTriggerLabel(task: TaskOverview): string {
    if (task.task.kind === "service") {
        const instances = Math.max(1, task.task.instances ?? 1);
        return instances > 1 ? `Service ×${String(instances)}` : "Service";
    }

    if (task.task.cron) {
        return humanizeCron(task.task.cron).humanized;
    }

    return task.isApiOnly ? "API trigger" : "Manual trigger";
}

// taskTriggerIsHumanizedCron reports whether the trigger label is plain
// English (proportional font) rather than a raw cron expression (mono).
export function taskTriggerIsHumanizedCron(task: TaskOverview): boolean {
    if (task.task.kind === "service" || !task.task.cron) {
        return false;
    }
    return humanizeCron(task.task.cron).isHumanized;
}

export function formatRunStartedLabel(run: Run, now: Date = new Date()): string {
    return formatRelativeTime(run.startedAt ?? run.createdAt, now);
}

export function formatRunDurationLabel(run: Run): string {
    return runDuration(run) ?? "Starting";
}
