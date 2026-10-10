// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Task } from "@runwisp/common";
import { taskDetailSections } from "./task-details";

const MINUTE_NS = 60e9;

function makeTask(overrides: Partial<Task> = {}): Task {
    return {
        name: "backup",
        manualTrigger: true,
        autostart: true,
        runOnStart: false,
        ...overrides,
    };
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

    it("lists env values but never secrets", () => {
        const task = makeTask({ env: { B: "2", A: "1" }, secretsFile: "/etc/rw.env" });
        expect(section(task, "Environment")?.rows).toEqual([
            { key: "env.A", value: '"1"' },
            { key: "env.B", value: '"2"' },
            { key: "secrets_file", value: '"/etc/rw.env"', note: "values not shown" },
        ]);
    });

    it("leaves out sections that don't apply", () => {
        const titles = taskDetailSections(makeTask()).map((s) => s.title);
        expect(titles).toEqual([]);
        const svc = taskDetailSections(makeTask({ kind: "service", instances: 2 })).map(
            (s) => s.title,
        );
        expect(svc).toEqual(["Service"]);
    });
});
