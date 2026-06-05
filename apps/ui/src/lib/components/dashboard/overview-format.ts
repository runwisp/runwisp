// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import {
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    getRunStatusConfig,
    humanizeCron,
    runDuration,
} from "@runwisp/ui";
import type { TaskOverview } from "./overview.js";
import type { Run, Task } from "@runwisp/common";

export function pluralize(count: number): string {
    return count === 1 ? "" : "s";
}

export function formatTaskDescription(task: Task): string {
    return task.description ?? "No description yet. Open the task to review its execution details.";
}

export function formatTaskLastRunLabel(task: TaskOverview, now: Date = new Date()): string {
    if (!task.lastRun) {
        return "No runs yet";
    }

    return formatRelativeTimeWithAbsolute(task.lastRun.start_at ?? task.lastRun.created_at, now);
}

export function formatTaskLastResultLabel(task: TaskOverview): string {
    if (!task.lastStatus) {
        return "No runs yet";
    }

    return formatStatusLabel(task.lastStatus);
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

export function getTaskStatusDot(status: TaskOverview["lastStatus"]): string {
    if (!status) {
        return "bg-mist-300";
    }

    return getRunStatusConfig(status).dot.split(" ")[0] ?? "bg-mist-300";
}

export function formatRunStartedLabel(run: Run, now: Date = new Date()): string {
    return formatRelativeTime(run.start_at ?? run.created_at, now);
}

export function formatRunDurationLabel(run: Run): string {
    return runDuration(run) ?? "Starting";
}

export function formatStatusLabel(status: string): string {
    return status
        .split("-")
        .map((segment) => segment.charAt(0).toUpperCase() + segment.slice(1))
        .join(" ");
}

export function formatTriggeredByLabel(triggeredBy: Run["triggered_by"]): string {
    if (triggeredBy === "api") {
        return "API";
    }
    if (triggeredBy === "cron") {
        return "Cron";
    }
    if (triggeredBy === "service") {
        return "Service";
    }

    return "Cloud";
}
