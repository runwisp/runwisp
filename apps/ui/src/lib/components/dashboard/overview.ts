// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { displayStatus, type RunStatus, type Run, type Task } from "@runwisp/common";

export type TaskWithId = Task & { id: string };

export type OverviewTaskState = "attention" | "running" | "scheduled" | "manual" | "idle";
export type OverviewTaskFilter = "all" | "attention" | "running" | "scheduled" | "manual";
export type OverviewTaskSortKey = "attention" | "last_activity" | "next_run" | "name";

export interface TaskOverview {
    task: TaskWithId;
    lastRun: Run | undefined;
    lastStatus: RunStatus | undefined;
    state: OverviewTaskState;
    nextRunMs: number | undefined;
    isApiOnly: boolean;
}

export interface OverviewSummary {
    totalTasks: number;
    activeRuns: number;
    attentionTasks: number;
    scheduledTasks: number;
    manualTasks: number;
}

export type OverviewTaskCounts = Record<OverviewTaskFilter, number>;

const ATTENTION_STATUSES = new Set<RunStatus>([
    "failed",
    "crashed",
    "stopped",
    "timeout",
    "log_overflow",
    "missed",
    "start_failed",
]);
const TASK_STATE_ORDER: Record<OverviewTaskState, number> = {
    attention: 0,
    running: 1,
    scheduled: 2,
    manual: 3,
    idle: 4,
};
const LOWEST_PRIORITY_TIME = -1;

export function buildTaskOverviews(
    tasks: TaskWithId[],
    recentRuns: Run[],
    runningRuns: Run[],
): TaskOverview[] {
    const recentRunsByTask = buildLatestRunByTask(recentRuns, (run) => toTimestamp(run.createdAt));
    const runningRunsByTask = buildLatestRunByTask(
        runningRuns,
        (run) => toTimestamp(run.startAt) ?? toTimestamp(run.createdAt),
    );

    return tasks.map((task) => {
        const activeRun = runningRunsByTask.get(task.name);
        const lastRun = activeRun ?? recentRunsByTask.get(task.name);
        const lastStatus = lastRun ? displayStatus(lastRun.status, lastRun.endReason) : undefined;
        const nextRunMs = toTimestamp(task.nextRunAt);
        const isApiOnly = task.apiTrigger && !task.cron;

        let state: OverviewTaskState = "idle";
        if (activeRun) {
            state = "running";
        } else if (lastStatus && ATTENTION_STATUSES.has(lastStatus)) {
            state = "attention";
        } else if (nextRunMs !== undefined) {
            state = "scheduled";
        } else if (isApiOnly) {
            state = "manual";
        }

        return {
            task,
            lastRun,
            lastStatus,
            state,
            nextRunMs,
            isApiOnly,
        };
    });
}

export function buildOverviewSummary(
    taskOverviews: TaskOverview[],
    runningRuns: Run[],
): OverviewSummary {
    return {
        totalTasks: taskOverviews.length,
        activeRuns: runningRuns.length,
        attentionTasks: taskOverviews.filter((task) => task.state === "attention").length,
        scheduledTasks: taskOverviews.filter((task) => task.nextRunMs !== undefined).length,
        manualTasks: taskOverviews.filter((task) => task.isApiOnly && task.nextRunMs === undefined)
            .length,
    };
}

export function countTaskOverviews(taskOverviews: TaskOverview[]): OverviewTaskCounts {
    return {
        all: taskOverviews.length,
        attention: taskOverviews.filter((task) => task.state === "attention").length,
        running: taskOverviews.filter((task) => task.state === "running").length,
        scheduled: taskOverviews.filter((task) => task.nextRunMs !== undefined).length,
        manual: taskOverviews.filter((task) => task.isApiOnly && task.nextRunMs === undefined)
            .length,
    };
}

export function filterTaskOverviews(
    taskOverviews: TaskOverview[],
    searchQuery: string,
    filter: OverviewTaskFilter,
    sortBy: OverviewTaskSortKey,
): TaskOverview[] {
    const normalizedQuery = searchQuery.trim().toLowerCase();

    const filtered = taskOverviews.filter((task) => {
        if (!matchesFilter(task, filter)) {
            return false;
        }
        if (!normalizedQuery) {
            return true;
        }
        return matchesSearch(task, normalizedQuery);
    });

    return sortTaskOverviews(filtered, sortBy);
}

export function sortRunsByStartDesc(runs: Run[]): Run[] {
    return [...runs].sort((left, right) => {
        const leftTime =
            toTimestamp(left.startAt) ?? toTimestamp(left.createdAt) ?? LOWEST_PRIORITY_TIME;
        const rightTime =
            toTimestamp(right.startAt) ?? toTimestamp(right.createdAt) ?? LOWEST_PRIORITY_TIME;
        return rightTime - leftTime;
    });
}

function lastTaskActivityAt(task: TaskOverview): number | undefined {
    return toTimestamp(task.lastRun?.startAt) ?? toTimestamp(task.lastRun?.createdAt);
}

function buildLatestRunByTask(
    runs: Run[],
    getTimestamp: (run: Run) => number | undefined,
): Map<string, Run> {
    const sortedRuns = [...runs].sort((left, right) => {
        const leftTime = getTimestamp(left) ?? LOWEST_PRIORITY_TIME;
        const rightTime = getTimestamp(right) ?? LOWEST_PRIORITY_TIME;
        return rightTime - leftTime;
    });

    const runsByTask = new Map<string, Run>();
    for (const run of sortedRuns) {
        if (!runsByTask.has(run.taskName)) {
            runsByTask.set(run.taskName, run);
        }
    }
    return runsByTask;
}

function matchesFilter(task: TaskOverview, filter: OverviewTaskFilter): boolean {
    switch (filter) {
        case "all":
            return true;
        case "scheduled":
            return task.nextRunMs !== undefined;
        case "manual":
            return task.isApiOnly && task.nextRunMs === undefined;
        default:
            return task.state === filter;
    }
}

function matchesSearch(task: TaskOverview, query: string): boolean {
    const group = task.task.group?.toLowerCase() ?? "";
    const description = task.task.description?.toLowerCase() ?? "";
    const cron = task.task.cron?.toLowerCase() ?? "";

    return [task.task.name.toLowerCase(), group, description, cron].some((value) =>
        value.includes(query),
    );
}

function sortTaskOverviews(
    taskOverviews: TaskOverview[],
    sortBy: OverviewTaskSortKey,
): TaskOverview[] {
    const sorted = [...taskOverviews];

    if (sortBy === "name") {
        return sorted.sort((left, right) => left.task.name.localeCompare(right.task.name));
    }

    if (sortBy === "next_run") {
        return sorted.sort((left, right) => {
            const nextRunComparison = compareOptionalAscending(left.nextRunMs, right.nextRunMs);
            if (nextRunComparison !== 0) {
                return nextRunComparison;
            }

            const recentRunComparison = compareOptionalDescending(
                lastTaskActivityAt(left),
                lastTaskActivityAt(right),
            );
            if (recentRunComparison !== 0) {
                return recentRunComparison;
            }

            return left.task.name.localeCompare(right.task.name);
        });
    }

    if (sortBy === "last_activity") {
        return sorted.sort((left, right) => {
            const recentRunComparison = compareOptionalDescending(
                lastTaskActivityAt(left),
                lastTaskActivityAt(right),
            );
            if (recentRunComparison !== 0) {
                return recentRunComparison;
            }

            const nextRunComparison = compareOptionalAscending(left.nextRunMs, right.nextRunMs);
            if (nextRunComparison !== 0) {
                return nextRunComparison;
            }

            return left.task.name.localeCompare(right.task.name);
        });
    }

    return sorted.sort((left, right) => {
        const stateComparison = TASK_STATE_ORDER[left.state] - TASK_STATE_ORDER[right.state];
        if (stateComparison !== 0) {
            return stateComparison;
        }

        const recentRunComparison = compareOptionalDescending(
            lastTaskActivityAt(left),
            lastTaskActivityAt(right),
        );
        if (recentRunComparison !== 0) {
            return recentRunComparison;
        }

        const nextRunComparison = compareOptionalAscending(left.nextRunMs, right.nextRunMs);
        if (nextRunComparison !== 0) {
            return nextRunComparison;
        }

        return left.task.name.localeCompare(right.task.name);
    });
}

function compareOptionalAscending(left: number | undefined, right: number | undefined): number {
    if (left === right) {
        return 0;
    }
    if (left === undefined) {
        return 1;
    }
    if (right === undefined) {
        return -1;
    }
    return left - right;
}

function compareOptionalDescending(left: number | undefined, right: number | undefined): number {
    if (left === right) {
        return 0;
    }
    if (left === undefined) {
        return 1;
    }
    if (right === undefined) {
        return -1;
    }
    return right - left;
}

function toTimestamp(value: string | undefined): number | undefined {
    if (!value) {
        return undefined;
    }

    const timestamp = new Date(value).getTime();
    return Number.isNaN(timestamp) ? undefined : timestamp;
}
