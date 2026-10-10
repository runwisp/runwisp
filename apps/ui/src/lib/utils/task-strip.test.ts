// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Run, Task } from "@runwisp/common";
import { taskStrip, type TaskStripModel } from "./task-strip";

const NOW = new Date("2026-10-09T12:00:00Z");

function makeTask(overrides: Partial<Task> = {}): Task {
    return {
        name: "backup",
        cron: "15 3 * * *",
        manualTrigger: true,
        autostart: true,
        runOnStart: false,
        ...overrides,
    };
}

function makeRun(overrides: Partial<Run> = {}): Run {
    return {
        id: "run-1",
        taskName: "backup",
        createdAt: "2026-10-09T11:59:00Z",
        startedAt: "2026-10-09T11:59:00Z",
        status: "ended",
        endReason: "succeeded",
        triggeredBy: "cron",
        exitCode: 0,
        instanceIndex: 0,
        retryAttempt: 0,
        isFailure: false,
        ...overrides,
    };
}

function service(
    overrides: Partial<Task> = {},
    instances: NonNullable<Task["service"]>["instances"] = [],
): Task {
    const list = instances ?? [];
    return makeTask({
        cron: "",
        kind: "service",
        instances: 2,
        restartAttempts: 5,
        lastRun: makeRun({ id: "last" }),
        service: {
            state: "running",
            desiredInstances: 2,
            runningInstances: list.filter((i) => i.state === "running").length,
            instances: list,
        },
        ...overrides,
    });
}

const up = (index: number) => ({
    index,
    state: "running" as const,
    startedAt: "2026-10-06T02:00:00Z",
    restartCount: 0,
    startFails: 0,
});

function strip(task: Task, running?: Run, schedulingActive = true): TaskStripModel {
    return taskStrip({ task, running, schedulingActive, now: NOW });
}

const text = (parts: TaskStripModel["sentence"]) => parts.map((p) => p.text).join("");
const labels = (m: TaskStripModel) => m.actions.map((a) => a.label);
const primaries = (m: TaskStripModel) => m.actions.filter((a) => a.primary).map((a) => a.label);

describe("taskStrip: cron tasks", () => {
    it("is scheduled, with the schedule and next run, Run filled and Pause beside it", () => {
        const m = strip(makeTask({ nextRunAt: "2026-10-10T03:15:00Z" }));
        expect(m.badge).toEqual({ text: "Scheduled", kind: "scheduled" });
        expect(text(m.sentence)).toMatch(/^At 03:15 AM · next run in 15 hours/);
        expect(labels(m)).toEqual(["Run now", "Pause"]);
        expect(primaries(m)).toEqual(["Run now"]);
    });

    it("says the last run failed and opens it", () => {
        const m = strip(makeTask({ lastRun: makeRun({ isFailure: true, endReason: "failed" }) }));
        expect(m.badge).toMatchObject({ text: "Last run failed", kind: "failed", runId: "run-1" });
    });

    it("puts a running run above everything, paused included", () => {
        const run = makeRun({ id: "live", status: "running" });
        const m = strip(makeTask({ pausedAt: "2026-10-07T14:02:00Z" }), run);
        expect(m.badge).toMatchObject({ text: "Running · 1m", kind: "running", runId: "live" });
        expect(primaries(m)).toEqual(["Resume schedule"]);
    });

    it("when paused, says since when and fills Resume", () => {
        const m = strip(makeTask({ pausedAt: "2026-10-07T14:02:00Z" }));
        expect(m.badge).toEqual({ text: "Paused", kind: "paused" });
        expect(text(m.sentence)).toContain("is skipped, manual runs still work");
        expect(m.sentence.find((p) => p.struck === true)?.text).toBe("At 03:15 AM");
        expect(labels(m)).toEqual(["Run now", "Resume schedule"]);
        expect(primaries(m)).toEqual(["Resume schedule"]);
    });

    it("when held by cron, explains it and can't be paused", () => {
        const m = strip(makeTask({ heldBy: "cron" }));
        expect(m.badge).toEqual({ text: "Held by cron", kind: "held" });
        expect(m.link).toEqual({ text: "How to hand it over", held: true });
        expect(labels(m)).toEqual(["Run now"]);
        expect(primaries(m)).toEqual([]);
    });

    it("in station mode, leaves the schedule to the station", () => {
        const m = strip(makeTask(), undefined, false);
        expect(m.badge.text).toBe("Scheduled by Station");
        expect(labels(m)).toEqual(["Run here"]);
    });

    it("has no actions when manual_trigger is off", () => {
        expect(strip(makeTask({ manualTrigger: false })).actions).toEqual([]);
    });

    it("says Run… when the task has parameters", () => {
        const m = strip(makeTask({ cron: "", parameters: [{ key: "org", kind: "env" }] }));
        expect(m.badge.text).toBe("Manual only");
        expect(labels(m)).toEqual(["Run…"]);
    });
});

describe("taskStrip: services", () => {
    it("all up: since when, Restart and Stop, nothing filled", () => {
        const m = strip(service({}, [up(0), up(1)]));
        expect(m.badge).toEqual({ text: "Running", kind: "up" });
        expect(text(m.sentence)).toMatch(/^2 of 2 instances up · since .+, 3 days ago$/);
        expect(labels(m)).toEqual(["Restart", "Stop service"]);
        expect(primaries(m)).toEqual([]);
    });

    it("an instance restarting: the restart count and last exit, nothing filled", () => {
        const restarting = {
            index: 1,
            state: "restarting" as const,
            restartCount: 2,
            startFails: 2,
            lastExitCode: 137,
        };
        const m = strip(service({}, [up(0), restarting]));
        expect(m.badge).toMatchObject({ text: "1 of 2 up", kind: "down", runId: "last" });
        expect(text(m.sentence)).toBe("Instance #2 is restarting · restart 2 of 5, last exit 137");
        expect(m.link).toEqual({ text: "Open its last run", runId: "last" });
        expect(primaries(m)).toEqual([]);
    });

    it("an instance gave up: Start #2 fills only that slot", () => {
        const fatal = {
            index: 1,
            state: "fatal" as const,
            restartCount: 5,
            startFails: 5,
            lastExitCode: -1,
        };
        const m = strip(service({}, [up(0), fatal]));
        expect(text(m.sentence)).toBe("Instance #2 gave up · 5 failed starts");
        expect(labels(m)).toEqual(["Start #2", "Restart", "Stop service"]);
        expect(primaries(m)).toEqual(["Start #2"]);
    });

    it("every instance gave up: Down, Start", () => {
        const fatal = (index: number) => ({
            index,
            state: "fatal" as const,
            restartCount: 5,
            startFails: 5,
            lastExitCode: 1,
        });
        const m = strip(service({}, [fatal(0), fatal(1)]));
        expect(m.badge).toMatchObject({ text: "Down", kind: "down", runId: "last" });
        expect(text(m.sentence)).toBe(
            "All 2 instances gave up · 5 failed starts each, last exit 1",
        );
        expect(labels(m)).toEqual(["Start"]);
    });

    it("stopped: what a daemon restart does depends on autostart", () => {
        const on = strip(service({ serviceStopped: true, autostart: true }));
        expect(on.badge).toEqual({ text: "Stopped", kind: "stopped" });
        expect(text(on.sentence)).toContain("or the daemon restarts (autostart is on)");
        expect(primaries(on)).toEqual(["Start"]);
        const off = strip(service({ serviceStopped: true, autostart: false }));
        expect(text(off.sentence)).toContain(
            "autostart is off, so a daemon restart leaves it down",
        );
    });

    it("a single instance reads as it, not instance #1", () => {
        const fatal = {
            index: 0,
            state: "fatal" as const,
            restartCount: 5,
            startFails: 5,
            lastExitCode: 2,
        };
        const m = strip(service({ instances: 1 }, [fatal]));
        expect(m.badge.text).toBe("Down");
        expect(text(m.sentence)).toBe("It gave up · 5 failed starts, last exit 2");
    });

    it("has no actions when manual_trigger is off", () => {
        expect(strip(service({ manualTrigger: false }, [up(0), up(1)])).actions).toEqual([]);
    });
});
