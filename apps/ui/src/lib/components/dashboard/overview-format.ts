// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import {
    formatRelativeTime,
    formatRelativeTimeWithAbsolute,
    getRunStatusConfig,
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

export function formatTaskLastRunLabel(task: TaskOverview): string {
    if (!task.lastRun) {
        return "No runs yet";
    }

    return formatRelativeTimeWithAbsolute(task.lastRun.start_at ?? task.lastRun.created_at);
}

export function formatTaskLastResultLabel(task: TaskOverview): string {
    if (!task.lastStatus) {
        return "No runs yet";
    }

    return formatStatusLabel(task.lastStatus);
}

export function formatTaskNextRunLabel(task: TaskOverview): string {
    if (task.nextRunMs !== undefined) {
        return formatRelativeTimeWithAbsolute(new Date(task.nextRunMs));
    }

    return task.isApiOnly ? "Manual only" : "Not scheduled";
}

export function formatTaskTriggerLabel(task: TaskOverview): string {
    if (task.task.trigger?.cron) {
        return task.task.trigger.cron;
    }

    return task.isApiOnly ? "API trigger" : "Manual trigger";
}

export function getTaskStatusDot(status: TaskOverview["lastStatus"]): string {
    if (!status) {
        return "bg-mist-300";
    }

    return getRunStatusConfig(status).dot.split(" ")[0] ?? "bg-mist-300";
}

export function formatRunStartedLabel(run: Run): string {
    return formatRelativeTime(run.start_at ?? run.created_at);
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

    return "Cloud";
}
