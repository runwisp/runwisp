// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Run } from "@runwisp/common";
import {
    buildTaskOverviews,
    buildOverviewSummary,
    countTaskOverviews,
    filterTaskOverviews,
    sortRunsByStartDesc,
    type TaskWithId,
    type TaskOverview,
} from "./overview";

function makeTask(name: string, overrides: Partial<TaskWithId> = {}): TaskWithId {
    return {
        id: name,
        name,
        api_trigger: false,
        autostart: true,
        ...overrides,
    };
}

function makeRun(taskName: string, overrides: Partial<Run> = {}): Run {
    return {
        id: `run-${taskName}`,
        task_name: taskName,
        created_at: new Date().toISOString(),
        status: "ended",
        triggered_by: "cron",
        exit_code: 0,
        instance_index: 0,
        retry_attempt: 0,
        ...overrides,
    };
}

// ─── buildTaskOverviews ───────────────────────────────────────────────────────

describe("buildTaskOverviews", () => {
    it("returns idle state when no runs and no schedule", () => {
        const tasks = [makeTask("backup-db")];
        const result = buildTaskOverviews(tasks, [], []);
        expect(result).toHaveLength(1);
        expect(result.at(0)?.state).toBe("idle");
        expect(result.at(0)?.lastRun).toBeUndefined();
    });

    it("sets running state when there is an active run", () => {
        const tasks = [makeTask("backup-db")];
        const run = makeRun("backup-db", { status: "running", start_at: new Date().toISOString() });
        const result = buildTaskOverviews(tasks, [], [run]);
        expect(result.at(0)?.state).toBe("running");
        expect(result.at(0)?.lastRun).toBeDefined();
    });

    it("sets attention state when last run was failed", () => {
        const tasks = [makeTask("backup-db")];
        const run = makeRun("backup-db", { status: "ended", end_reason: "failed" });
        const result = buildTaskOverviews(tasks, [run], []);
        expect(result.at(0)?.state).toBe("attention");
    });

    it("sets attention state for crashed", () => {
        const tasks = [makeTask("backup-db")];
        const run = makeRun("backup-db", { status: "ended", end_reason: "crashed" });
        const result = buildTaskOverviews(tasks, [run], []);
        expect(result.at(0)?.state).toBe("attention");
    });

    it("sets scheduled state when next_run_at is set", () => {
        const nextRun = new Date(Date.now() + 3600000).toISOString();
        const tasks = [makeTask("cron-job", { next_run_at: nextRun })];
        const result = buildTaskOverviews(tasks, [], []);
        expect(result.at(0)?.state).toBe("scheduled");
        expect(result.at(0)?.nextRunMs).toBeDefined();
    });

    it("sets manual state when api_trigger is true and no schedule", () => {
        const tasks = [makeTask("trigger-only", { api_trigger: true })];
        const result = buildTaskOverviews(tasks, [], []);
        expect(result.at(0)?.state).toBe("manual");
        expect(result.at(0)?.isApiOnly).toBe(true);
    });

    it("running state takes priority over attention (active run wins)", () => {
        const tasks = [makeTask("backup-db")];
        const failedRun = makeRun("backup-db", { status: "ended", end_reason: "failed" });
        const activeRun = makeRun("backup-db", {
            status: "running",
            start_at: new Date().toISOString(),
        });
        const result = buildTaskOverviews(tasks, [failedRun], [activeRun]);
        expect(result.at(0)?.state).toBe("running");
    });

    it("prefers most recent run by timestamp when multiple runs for same task", () => {
        const tasks = [makeTask("backup-db")];
        const older = makeRun("backup-db", {
            id: "r-old",
            created_at: "2024-01-01T00:00:00Z",
            status: "ended",
            end_reason: "success",
        });
        const newer = makeRun("backup-db", {
            id: "r-new",
            created_at: "2024-06-01T00:00:00Z",
            status: "ended",
            end_reason: "failed",
        });
        const result = buildTaskOverviews(tasks, [older, newer], []);
        expect(result.at(0)?.lastRun?.id).toBe("r-new");
        expect(result.at(0)?.state).toBe("attention");
    });
});

// ─── buildOverviewSummary ─────────────────────────────────────────────────────

describe("buildOverviewSummary", () => {
    it("counts running runs correctly", () => {
        const tasks = [makeTask("a"), makeTask("b")];
        const overviews = buildTaskOverviews(tasks, [], []);
        const runningRuns = [
            makeRun("a", { status: "running" }),
            makeRun("b", { status: "running" }),
        ];
        const summary = buildOverviewSummary(overviews, runningRuns);
        expect(summary.totalTasks).toBe(2);
        expect(summary.activeRuns).toBe(2);
    });

    it("counts attention tasks", () => {
        const tasks = [makeTask("a"), makeTask("b")];
        const failedRun = makeRun("a", { status: "ended", end_reason: "failed" });
        const overviews = buildTaskOverviews(tasks, [failedRun], []);
        const summary = buildOverviewSummary(overviews, []);
        expect(summary.attentionTasks).toBe(1);
    });
});

// ─── countTaskOverviews ───────────────────────────────────────────────────────

describe("countTaskOverviews", () => {
    it("returns zero counts for empty list", () => {
        const counts = countTaskOverviews([]);
        expect(counts.all).toBe(0);
        expect(counts.attention).toBe(0);
    });

    it("counts manual tasks (api-only, no schedule)", () => {
        const task = makeTask("api-task", { api_trigger: true });
        const overviews = buildTaskOverviews([task], [], []);
        const counts = countTaskOverviews(overviews);
        expect(counts.manual).toBe(1);
        expect(counts.scheduled).toBe(0);
    });
});

// ─── filterTaskOverviews ──────────────────────────────────────────────────────

describe("filterTaskOverviews", () => {
    function makeOverview(name: string, state: TaskOverview["state"]): TaskOverview {
        return {
            task: makeTask(name),
            lastRun: undefined,
            lastStatus: undefined,
            state,
            nextRunMs: state === "scheduled" ? Date.now() + 3600000 : undefined,
            isApiOnly: state === "manual",
        };
    }

    it("returns all tasks for 'all' filter with empty search", () => {
        const overviews = [makeOverview("a", "running"), makeOverview("b", "idle")];
        const result = filterTaskOverviews(overviews, "", "all", "attention");
        expect(result).toHaveLength(2);
    });

    it("filters by running state", () => {
        const overviews = [makeOverview("a", "running"), makeOverview("b", "idle")];
        const result = filterTaskOverviews(overviews, "", "running", "attention");
        expect(result).toHaveLength(1);
        expect(result.at(0)?.task.name).toBe("a");
    });

    it("filters by attention state", () => {
        const overviews = [makeOverview("a", "attention"), makeOverview("b", "idle")];
        const result = filterTaskOverviews(overviews, "", "attention", "attention");
        expect(result).toHaveLength(1);
    });

    it("filters by manual state", () => {
        const overviews = [makeOverview("a", "manual"), makeOverview("b", "running")];
        const result = filterTaskOverviews(overviews, "", "manual", "attention");
        expect(result).toHaveLength(1);
    });

    it("filters by scheduled state", () => {
        const overviews = [makeOverview("a", "scheduled"), makeOverview("b", "idle")];
        const result = filterTaskOverviews(overviews, "", "scheduled", "attention");
        expect(result).toHaveLength(1);
    });

    it("applies search query to filter by name", () => {
        const overviews = [
            makeOverview("backup-db", "idle"),
            makeOverview("process-queue", "idle"),
        ];
        const result = filterTaskOverviews(overviews, "backup", "all", "name");
        expect(result).toHaveLength(1);
        expect(result.at(0)?.task.name).toBe("backup-db");
    });

    it("case-insensitive search query", () => {
        const overviews = [makeOverview("Backup-DB", "idle")];
        const result = filterTaskOverviews(overviews, "backup", "all", "name");
        expect(result).toHaveLength(1);
    });

    it("search query with no match returns empty", () => {
        const overviews = [makeOverview("backup-db", "idle")];
        const result = filterTaskOverviews(overviews, "nonexistent", "all", "name");
        expect(result).toHaveLength(0);
    });

    it("sorts by name when sortBy=name", () => {
        const overviews = [makeOverview("z-task", "idle"), makeOverview("a-task", "idle")];
        const result = filterTaskOverviews(overviews, "", "all", "name");
        expect(result.at(0)?.task.name).toBe("a-task");
        expect(result.at(1)?.task.name).toBe("z-task");
    });

    it("sorts by attention state first (default sort)", () => {
        const overviews = [
            makeOverview("idle-task", "idle"),
            makeOverview("att-task", "attention"),
        ];
        const result = filterTaskOverviews(overviews, "", "all", "attention");
        expect(result.at(0)?.task.name).toBe("att-task");
    });

    it("sorts by last_activity when sortBy=last_activity", () => {
        const overviews = [makeOverview("a", "idle"), makeOverview("b", "idle")];
        const result = filterTaskOverviews(overviews, "", "all", "last_activity");
        expect(result).toHaveLength(2);
    });

    it("sorts by next_run when sortBy=next_run", () => {
        const overviews = [makeOverview("a", "idle"), makeOverview("b", "scheduled")];
        const result = filterTaskOverviews(overviews, "", "all", "next_run");
        expect(result).toHaveLength(2);
    });
});

// ─── sortRunsByStartDesc ──────────────────────────────────────────────────────

describe("sortRunsByStartDesc", () => {
    it("sorts runs with start_at by most recent first", () => {
        const old = makeRun("t", { id: "r-old", start_at: "2024-01-01T00:00:00Z" });
        const recent = makeRun("t", { id: "r-new", start_at: "2024-06-01T00:00:00Z" });
        const result = sortRunsByStartDesc([old, recent]);
        expect(result.at(0)?.id).toBe("r-new");
        expect(result.at(1)?.id).toBe("r-old");
    });

    it("sorts runs without start_at by created_at", () => {
        const old = makeRun("t", { id: "r-old", created_at: "2024-01-01T00:00:00Z" });
        const recent = makeRun("t", { id: "r-new", created_at: "2024-06-01T00:00:00Z" });
        const result = sortRunsByStartDesc([old, recent]);
        expect(result.at(0)?.id).toBe("r-new");
    });

    it("falls back to LOWEST_PRIORITY_TIME when both start_at and created_at are invalid", () => {
        // With two bad runs, V8's sort will eventually call compareFn(bad1, bad2) where
        // both left and right have invalid timestamps, triggering LOWEST_PRIORITY_TIME
        // for both leftTime (line 136) and rightTime (line 138).
        const bad1 = makeRun("t", { id: "r-bad1", start_at: "x", created_at: "x" });
        const bad2 = makeRun("t", { id: "r-bad2", start_at: "x", created_at: "x" });
        const good = makeRun("t", { id: "r-good", start_at: "2024-06-01T00:00:00Z" });
        const result = sortRunsByStartDesc([good, bad1, bad2]);
        expect(result.at(0)?.id).toBe("r-good");
    });

    it("does not mutate the input array", () => {
        const runs = [makeRun("t", { id: "a" }), makeRun("t", { id: "b" })];
        const original = [...runs];
        sortRunsByStartDesc(runs);
        expect(runs).toEqual(original);
    });
});

// ─── buildTaskOverviews: ?? branches ─────────────────────────────────────────

describe("buildTaskOverviews ?? branches", () => {
    it("running run without start_at falls back to created_at; invalid created_at uses LOWEST_PRIORITY", () => {
        // Two running runs force the sort comparator. V8 calls compareFn(arr[1], arr[0]).
        // badRun at index 0 → V8 calls compareFn(goodRun, badRun):
        //   line 55: goodRun.start_at=undefined → ?? toTimestamp(goodRun.created_at) evaluated
        //   line 152: badRun both timestamps invalid → ?? LOWEST_PRIORITY_TIME triggered
        const tasks = [makeTask("t1"), makeTask("t2")];
        const goodRun = makeRun("t1", { created_at: "2024-06-01T00:00:00Z" });
        const badRun = makeRun("t2", { created_at: "invalid" }); // both start_at and created_at invalid
        const result = buildTaskOverviews(tasks, [], [badRun, goodRun]);
        expect(result.some((r) => r.state === "running")).toBe(true);
    });

    it("recent run with invalid created_at falls back to LOWEST_PRIORITY for ordering", () => {
        const tasks = [makeTask("t")];
        const bad = makeRun("t", { id: "r-bad", created_at: "invalid" });
        const good = makeRun("t", {
            id: "r-good",
            created_at: "2024-06-01T00:00:00Z",
            status: "ended",
            end_reason: "success",
        });
        // good at index 0, bad at index 1: V8 calls compareFn(bad, good) so bad is `left`,
        // triggering the ?? LOWEST_PRIORITY_TIME fallback for leftTime (line 152).
        const result = buildTaskOverviews(tasks, [good, bad], []);
        expect(result.at(0)?.lastRun?.id).toBe("r-good");
    });

    it("isApiOnly=true with nextRunMs set → not counted as manual", () => {
        const future = new Date(Date.now() + 3600000).toISOString();
        const tasks = [makeTask("api-cron", { api_trigger: true, next_run_at: future })];
        const result = buildTaskOverviews(tasks, [], []);
        // isApiOnly = api_trigger && !cron = true && !undefined = true; state = "scheduled"
        expect(result.at(0)?.state).toBe("scheduled");
        expect(result.at(0)?.isApiOnly).toBe(true);
        const summary = buildOverviewSummary(result, []);
        // manualTasks = filter(isApiOnly && nextRunMs === undefined) → false since nextRunMs is set
        expect(summary.manualTasks).toBe(0);
    });
});

// ─── sort tie-breaking paths ──────────────────────────────────────────────────

describe("filterTaskOverviews sort tie-breaking", () => {
    function makeOverview(
        name: string,
        state: TaskOverview["state"],
        opts: Partial<TaskOverview> = {},
    ): TaskOverview {
        return {
            task: makeTask(name),
            lastRun: undefined,
            lastStatus: undefined,
            state,
            nextRunMs: state === "scheduled" ? Date.now() + 3600000 : undefined,
            isApiOnly: state === "manual",
            ...opts,
        };
    }

    it("next_run sort: equal nextRunMs falls through to lastActivity tiebreak", () => {
        const shared = Date.now() + 3600000;
        const a = makeOverview("a-task", "scheduled", {
            nextRunMs: shared,
            lastRun: makeRun("a-task", { start_at: "2024-05-01T00:00:00Z" }),
        });
        const b = makeOverview("b-task", "scheduled", {
            nextRunMs: shared,
            lastRun: makeRun("b-task", { start_at: "2024-04-01T00:00:00Z" }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "next_run");
        // same nextRunMs → sort by lastActivity desc → a (May) before b (Apr)
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("next_run sort: equal nextRunMs and equal lastActivity falls through to name", () => {
        const shared = Date.now() + 3600000;
        const a = makeOverview("a-task", "scheduled", { nextRunMs: shared });
        const b = makeOverview("b-task", "scheduled", { nextRunMs: shared });
        const result = filterTaskOverviews([b, a], "", "all", "next_run");
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("next_run sort: task with nextRunMs before task without (right undefined)", () => {
        const withNext = makeOverview("z-with", "scheduled", { nextRunMs: Date.now() + 100 });
        const noNext = makeOverview("a-without", "idle", { nextRunMs: undefined });
        const result = filterTaskOverviews([withNext, noNext], "", "all", "next_run");
        // withNext (number) before noNext (undefined) → compareOptionalAscending(number, undefined) → -1
        expect(result.at(0)?.task.name).toBe("z-with");
    });

    it("last_activity sort: equal lastActivity falls through to nextRun tiebreak", () => {
        const sharedTime = "2024-05-01T00:00:00Z";
        const a = makeOverview("a-task", "scheduled", {
            nextRunMs: Date.now() + 1000,
            lastRun: makeRun("a-task", { start_at: sharedTime }),
        });
        const b = makeOverview("b-task", "scheduled", {
            nextRunMs: Date.now() + 9000,
            lastRun: makeRun("b-task", { start_at: sharedTime }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "last_activity");
        // equal lastActivity → sort by nextRun asc → a (sooner) before b
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("last_activity sort: equal lastActivity and nextRun falls through to name", () => {
        const sharedTime = "2024-05-01T00:00:00Z";
        const shared = Date.now() + 3600000;
        const a = makeOverview("a-task", "scheduled", {
            nextRunMs: shared,
            lastRun: makeRun("a-task", { start_at: sharedTime }),
        });
        const b = makeOverview("b-task", "scheduled", {
            nextRunMs: shared,
            lastRun: makeRun("b-task", { start_at: sharedTime }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "last_activity");
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("attention sort: same state falls through to lastActivity tiebreak", () => {
        const a = makeOverview("a-task", "idle", {
            lastRun: makeRun("a-task", { start_at: "2024-06-01T00:00:00Z" }),
        });
        const b = makeOverview("b-task", "idle", {
            lastRun: makeRun("b-task", { start_at: "2024-05-01T00:00:00Z" }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "attention");
        // same state (idle) → sort by lastActivity desc → a (June) before b (May)
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("attention sort: same state and lastActivity falls through to nextRun tiebreak", () => {
        const sharedTime = "2024-05-01T00:00:00Z";
        const a = makeOverview("a-task", "idle", {
            nextRunMs: Date.now() + 1000,
            lastRun: makeRun("a-task", { start_at: sharedTime }),
        });
        const b = makeOverview("b-task", "idle", {
            nextRunMs: Date.now() + 9000,
            lastRun: makeRun("b-task", { start_at: sharedTime }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "attention");
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("attention sort: same state, lastActivity, nextRun falls through to name", () => {
        const sharedTime = "2024-05-01T00:00:00Z";
        const shared = Date.now() + 3600000;
        const a = makeOverview("a-task", "idle", {
            nextRunMs: shared,
            lastRun: makeRun("a-task", { start_at: sharedTime }),
        });
        const b = makeOverview("b-task", "idle", {
            nextRunMs: shared,
            lastRun: makeRun("b-task", { start_at: sharedTime }),
        });
        const result = filterTaskOverviews([b, a], "", "all", "attention");
        expect(result.at(0)?.task.name).toBe("a-task");
    });

    it("compareOptionalDescending: right undefined → left first (right===undefined branch)", () => {
        // noRun first → V8 sort calls compareFn(withRun, noRun):
        // compareOptionalDescending(timestamp, undefined) hits line 281 (right===undefined).
        const withRun = makeOverview("z-with", "idle", {
            lastRun: makeRun("z-with", { start_at: "2024-06-01T00:00:00Z" }),
        });
        const noRun = makeOverview("a-without", "idle");
        const result = filterTaskOverviews([noRun, withRun], "", "all", "last_activity");
        expect(result.at(0)?.task.name).toBe("z-with");
    });

    it("compareOptionalDescending: left undefined → right first (left===undefined branch)", () => {
        // withRun first → V8 sort calls compareFn(noRun, withRun):
        // compareOptionalDescending(undefined, timestamp) hits line 278 (left===undefined).
        const withRun = makeOverview("a-with", "idle", {
            lastRun: makeRun("a-with", { start_at: "2024-06-01T00:00:00Z" }),
        });
        const noRun = makeOverview("z-without", "idle");
        const result = filterTaskOverviews([withRun, noRun], "", "all", "last_activity");
        expect(result.at(0)?.task.name).toBe("a-with");
    });
});
