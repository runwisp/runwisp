// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Task } from "@runwisp/common";
import { TASK_DEFAULTS } from "./__task_defaults";
import { taskDetailSections } from "./task-details";

const SECOND_NS = 1e9;
const MINUTE_NS = 60 * SECOND_NS;

/** A [tasks.*] entry as the API serves it with every key left out: resolved defaults. */
function makeTask(overrides: Partial<Task> = {}): Task {
    return { ...TASK_DEFAULTS.task, name: "backup", ...overrides };
}

/** A [services.*] entry as the API serves it with every key left out. */
function makeService(overrides: Partial<Task> = {}): Task {
    return { ...TASK_DEFAULTS.service, name: "web", ...overrides };
}

const section = (task: Task, title: string) =>
    taskDetailSections(task).find((s) => s.title === title);

describe("taskDetailSections", () => {
    it("starts with the command", () => {
        const [first] = taskDetailSections(makeTask({ run: "pg_dump db | gzip" }));
        expect(first).toEqual({ title: "Command", command: "pg_dump db | gzip", rows: [] });
    });

    it("uses runwisp.toml keys and TOML values", () => {
        const task = makeTask({
            cron: "15 3 * * *",
            timezone: "Europe/Bratislava",
            timeout: 30 * MINUTE_NS,
            retryAttempts: 2,
            retryBackoff: "exponential",
            retryDelay: 0.5 * MINUTE_NS,
        });
        expect(section(task, "Schedule")?.rows).toEqual([
            { key: "cron", value: '"15 3 * * *"', note: "At 03:15 AM" },
            { key: "timezone", value: '"Europe/Bratislava"' },
        ]);
        expect(section(task, "Execution")?.rows).toEqual([
            { key: "timeout", value: '"30m"' },
            { key: "retry_attempts", value: "2", note: "exponential, from 30s" },
        ]);
    });

    it("hides keys left at their defaults", () => {
        const cron = taskDetailSections(makeTask({ cron: "15 3 * * *" }));
        expect(cron.map((s) => s.title)).toEqual(["Schedule"]);
        expect(cron[0]?.rows.map((r) => r.key)).toEqual(["cron"]);
        expect(taskDetailSections(makeService())).toEqual([]);
    });

    it("shows a key set to 0", () => {
        const task = makeTask({
            cron: "15 3 * * *",
            catchUp: 0,
            timeout: 0,
            maxQueued: 0,
            gracefulStop: 0,
            keepRuns: 0,
        });
        expect(section(task, "Schedule")?.rows).toContainEqual({ key: "catch_up", value: "0" });
        expect(section(task, "Execution")?.rows).toEqual([
            { key: "timeout", value: '"0s"' },
            { key: "max_queued", value: "0" },
            { key: "graceful_stop", value: '"0s"' },
        ]);
        expect(section(task, "Output and retention")?.rows).toEqual([
            { key: "keep_runs", value: "0" },
        ]);
        const svc = makeService({ restartAttempts: 0, restartDelay: 0 });
        expect(section(svc, "Service")?.rows).toEqual([
            {
                key: "restart_attempts",
                value: "0",
                note: "fast failures in a row, then it gives up",
            },
            { key: "restart_delay", value: '"0s"' },
        ]);
    });

    it("never shows a key the task's kind doesn't accept", () => {
        const svc = makeService({ maxConcurrent: 2, instances: 2, maxQueued: 5 });
        const keys = taskDetailSections(svc).flatMap((s) => s.rows.map((r) => r.key));
        expect(keys).toEqual(["instances"]);
    });

    it("lists env values but never secrets", () => {
        const task = makeTask({ env: { B: "2", A: "1" }, secretsFile: "/etc/rw.env" });
        expect(section(task, "Environment")?.rows).toEqual([
            { key: "env.A", value: '"1"' },
            { key: "env.B", value: '"2"' },
            { key: "secrets_file", value: '"/etc/rw.env"', note: "values not shown" },
        ]);
    });

    it("shows the source file as a path, not a key", () => {
        const task = makeTask({ sourceFile: "/etc/cron.d/backup" });
        expect(section(task, "Defined in")).toEqual({
            title: "Defined in",
            path: "/etc/cron.d/backup",
            rows: [],
        });
    });

    it("leaves out sections that don't apply", () => {
        expect(taskDetailSections(makeTask())).toEqual([]);
        const svc = taskDetailSections(makeService({ instances: 2 })).map((s) => s.title);
        expect(svc).toEqual(["Service"]);
    });
});
