// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Run, Task } from "@runwisp/common";
import type { TaskOverview, TaskWithId } from "./overview.js";
import {
    formatRunDurationLabel,
    formatTaskDescription,
    formatTaskLastResultLabel,
    formatTaskLastRunLabel,
    formatTaskNextRunLabel,
    formatTaskTriggerLabel,
    formatTriggeredByLabel,
    taskTriggerIsHumanizedCron,
} from "./overview-format";

function makeTask(overrides: Partial<Task> = {}): TaskWithId {
    return { id: "task-id-1", name: "my-task", api_trigger: false, autostart: true, ...overrides };
}

function makeRun(overrides: Partial<Run> = {}): Run {
    return {
        id: "run-1",
        task_name: "my-task",
        created_at: new Date("2024-01-01T00:00:00Z").toISOString(),
        status: "ended",
        exit_code: 0,
        triggered_by: "cron",
        retry_attempt: 0,
        instance_index: 0,
        ...overrides,
    };
}

function makeOverview(overrides: Partial<TaskOverview> = {}): TaskOverview {
    return {
        task: makeTask(),
        lastRun: undefined,
        lastStatus: undefined,
        nextRunMs: undefined,
        isApiOnly: false,
        state: "idle",
        ...overrides,
    };
}

describe("formatTaskDescription", () => {
    it("returns description when task has one", () => {
        const task = makeTask({ description: "My custom description" });
        expect(formatTaskDescription(task)).toBe("My custom description");
    });

    it("returns fallback when task has no description", () => {
        const task = makeTask({});
        expect(formatTaskDescription(task)).toBe(
            "No description yet. Open the task to review its execution details.",
        );
    });
});

describe("formatTaskLastRunLabel", () => {
    it("returns 'No runs yet' when lastRun is undefined", () => {
        const overview = makeOverview({ lastRun: undefined });
        expect(formatTaskLastRunLabel(overview)).toBe("No runs yet");
    });
});

describe("formatTaskLastResultLabel", () => {
    it("returns 'No runs yet' when lastStatus is undefined", () => {
        const overview = makeOverview({ lastStatus: undefined });
        expect(formatTaskLastResultLabel(overview)).toBe("No runs yet");
    });

    it("returns a formatted string when lastStatus is set", () => {
        const overview = makeOverview({ lastStatus: "success" });
        expect(formatTaskLastResultLabel(overview)).toBe("Success");
    });

    it("returns a formatted string when lastStatus is 'failed'", () => {
        const overview = makeOverview({ lastStatus: "failed" });
        expect(formatTaskLastResultLabel(overview)).toBe("Failed");
    });
});

describe("formatTaskNextRunLabel", () => {
    it("returns 'Always on' when task kind is service", () => {
        const overview = makeOverview({ task: makeTask({ kind: "service" }) });
        expect(formatTaskNextRunLabel(overview)).toBe("Always on");
    });

    it("returns 'Manual only' when nextRunMs is undefined and isApiOnly is true", () => {
        const overview = makeOverview({ nextRunMs: undefined, isApiOnly: true });
        expect(formatTaskNextRunLabel(overview)).toBe("Manual only");
    });

    it("returns 'Not scheduled' when nextRunMs is undefined and isApiOnly is false", () => {
        const overview = makeOverview({ nextRunMs: undefined, isApiOnly: false });
        expect(formatTaskNextRunLabel(overview)).toBe("Not scheduled");
    });

    it("ticks down as the supplied now advances", () => {
        const nextRun = new Date("2024-01-01T12:00:00Z");
        const overview = makeOverview({ nextRunMs: nextRun.getTime() });
        expect(formatTaskNextRunLabel(overview, new Date("2024-01-01T10:00:00Z"))).toMatch(
            /^in 2 hours/,
        );
        expect(formatTaskNextRunLabel(overview, new Date("2024-01-01T11:00:00Z"))).toMatch(
            /^in 1 hour/,
        );
    });
});

describe("formatTaskTriggerLabel", () => {
    it("returns 'Service' when task kind is service with 1 instance", () => {
        const overview = makeOverview({ task: makeTask({ kind: "service", instances: 1 }) });
        expect(formatTaskTriggerLabel(overview)).toBe("Service");
    });

    it("returns 'Service ×3' when task kind is service with 3 instances", () => {
        const overview = makeOverview({ task: makeTask({ kind: "service", instances: 3 }) });
        expect(formatTaskTriggerLabel(overview)).toBe("Service ×3");
    });

    it("returns the humanized cron when task has cron set", () => {
        const overview = makeOverview({ task: makeTask({ cron: "0 * * * *" }) });
        expect(formatTaskTriggerLabel(overview)).toBe("Every hour");
    });

    it("returns the raw cron when humanizing fails", () => {
        const overview = makeOverview({ task: makeTask({ cron: "not a cron" }) });
        expect(formatTaskTriggerLabel(overview)).toBe("not a cron");
    });

    it("returns 'API trigger' when no cron and isApiOnly is true", () => {
        const overview = makeOverview({ task: makeTask({}), isApiOnly: true });
        expect(formatTaskTriggerLabel(overview)).toBe("API trigger");
    });

    it("returns 'Manual trigger' when no cron and isApiOnly is false", () => {
        const overview = makeOverview({ task: makeTask({}), isApiOnly: false });
        expect(formatTaskTriggerLabel(overview)).toBe("Manual trigger");
    });
});

describe("taskTriggerIsHumanizedCron", () => {
    it("is true for a humanizable cron", () => {
        const overview = makeOverview({ task: makeTask({ cron: "*/5 * * * *" }) });
        expect(taskTriggerIsHumanizedCron(overview)).toBe(true);
    });

    it("is false for an unparseable cron", () => {
        const overview = makeOverview({ task: makeTask({ cron: "not a cron" }) });
        expect(taskTriggerIsHumanizedCron(overview)).toBe(false);
    });

    it("is false for services and cron-less tasks", () => {
        expect(
            taskTriggerIsHumanizedCron(makeOverview({ task: makeTask({ kind: "service" }) })),
        ).toBe(false);
        expect(taskTriggerIsHumanizedCron(makeOverview({ task: makeTask({}) }))).toBe(false);
    });
});

describe("formatRunDurationLabel", () => {
    it("returns 'Starting' when run has no end_at and no start_at", () => {
        const run = makeRun({});
        expect(formatRunDurationLabel(run)).toBe("Starting");
    });
});

describe("formatTriggeredByLabel", () => {
    it("returns 'API' for 'api'", () => {
        expect(formatTriggeredByLabel("api")).toBe("API");
    });

    it("returns 'Cron' for 'cron'", () => {
        expect(formatTriggeredByLabel("cron")).toBe("Cron");
    });

    it("returns 'Cloud' for 'cloud'", () => {
        expect(formatTriggeredByLabel("cloud")).toBe("Cloud");
    });

    it("returns 'Service' for 'service'", () => {
        expect(formatTriggeredByLabel("service")).toBe("Service");
    });

    it("returns 'Startup' for 'startup'", () => {
        expect(formatTriggeredByLabel("startup")).toBe("Startup");
    });
});
