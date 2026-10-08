// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { displayStatus, type RunStatus, type Run, type Task } from "@runwisp/common";

export type OverviewTaskState =
    "attention" | "running" | "paused" | "scheduled" | "manual" | "idle";
export type OverviewTaskFilter = "all" | "attention" | "running" | "scheduled" | "manual";
export type OverviewTaskSortKey = "attention" | "last_activity" | "next_run" | "name";

export interface TaskOverview {
    task: Task;
    lastRun: Run | undefined;
    lastStatus: RunStatus | undefined;
    state: OverviewTaskState;
    nextRunMs: number | undefined;
    isApiOnly: boolean;
}

export type OverviewTaskCounts = Record<OverviewTaskFilter, number>;

const TASK_STATE_ORDER: Record<OverviewTaskState, number> = {
    attention: 0,
    running: 1,
    paused: 2,
    scheduled: 3,
    manual: 4,
    idle: 5,
};
const LOWEST_PRIORITY_TIME = -1;

export function buildTaskOverviews(
    tasks: Task[],
    recentRuns: Run[],
    runningRuns: Run[],
): TaskOverview[] {
    const recentRunsByTask = buildLatestRunByTask(recentRuns, (run) => toTimestamp(run.createdAt));
    const runningRunsByTask = buildLatestRunByTask(runningRuns, runStartTime);

    return tasks.map((task) => {
        const activeRun = runningRunsByTask.get(task.name);
        const lastRun = activeRun ?? recentRunsByTask.get(task.name);
        const lastStatus = lastRun ? displayStatus(lastRun.status, lastRun.endReason) : undefined;
        const nextRunMs = toTimestamp(task.nextRunAt);
        const isApiOnly = task.manualTrigger && !task.cron;

        let state: OverviewTaskState = "idle";
        if (activeRun) {
            state = "running";
        } else if (lastRun?.isFailure === true) {
            state = "attention";
        } else if (task.pausedAt) {
            state = "paused";
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

export function countTaskOverviews(taskOverviews: TaskOverview[]): OverviewTaskCounts {
    const count = (filter: OverviewTaskFilter) =>
        taskOverviews.filter((task) => matchesFilter(task, filter)).length;
    return {
        all: count("all"),
        attention: count("attention"),
        running: count("running"),
        scheduled: count("scheduled"),
        manual: count("manual"),
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
    return [...runs].sort(
        (left, right) =>
            (runStartTime(right) ?? LOWEST_PRIORITY_TIME) -
            (runStartTime(left) ?? LOWEST_PRIORITY_TIME),
    );
}

/** When the run started, falling back to when it was created. */
function runStartTime(run: Run | undefined): number | undefined {
    return toTimestamp(run?.startedAt) ?? toTimestamp(run?.createdAt);
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

type TaskOverviewComparator = (left: TaskOverview, right: TaskOverview) => number;

/** Combines comparators, applying each in order until one returns non-zero. */
function compareBy(...comparators: TaskOverviewComparator[]): TaskOverviewComparator {
    return (left, right) => {
        for (const comparator of comparators) {
            const result = comparator(left, right);
            if (result !== 0) {
                return result;
            }
        }
        return 0;
    };
}

const byName: TaskOverviewComparator = (left, right) =>
    left.task.name.localeCompare(right.task.name);

const byNextRunAscending: TaskOverviewComparator = (left, right) =>
    compareOptional(left.nextRunMs, right.nextRunMs, 1);

const byLastActivityDescending: TaskOverviewComparator = (left, right) =>
    compareOptional(runStartTime(left.lastRun), runStartTime(right.lastRun), -1);

const byStateOrder: TaskOverviewComparator = (left, right) =>
    TASK_STATE_ORDER[left.state] - TASK_STATE_ORDER[right.state];

const SORT_COMPARATORS: Record<OverviewTaskSortKey, TaskOverviewComparator> = {
    name: byName,
    next_run: compareBy(byNextRunAscending, byLastActivityDescending, byName),
    last_activity: compareBy(byLastActivityDescending, byNextRunAscending, byName),
    attention: compareBy(byStateOrder, byLastActivityDescending, byNextRunAscending, byName),
};

function sortTaskOverviews(
    taskOverviews: TaskOverview[],
    sortBy: OverviewTaskSortKey,
): TaskOverview[] {
    return [...taskOverviews].sort(SORT_COMPARATORS[sortBy]);
}

/** Orders numbers by `direction` (1 ascending, -1 descending); undefined always sorts last. */
function compareOptional(
    left: number | undefined,
    right: number | undefined,
    direction: 1 | -1,
): number {
    if (left === right) {
        return 0;
    }
    if (left === undefined) {
        return 1;
    }
    if (right === undefined) {
        return -1;
    }
    return (left - right) * direction;
}

function toTimestamp(value: string | undefined): number | undefined {
    if (!value) {
        return undefined;
    }

    const timestamp = new Date(value).getTime();
    return Number.isNaN(timestamp) ? undefined : timestamp;
}
