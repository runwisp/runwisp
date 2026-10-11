// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import {
    createScheduledTask,
    localIso,
    type Logger,
    type RunContext,
    type TaskControl,
    type TaskFn,
    type TaskSettings,
} from "./scheduled-task.ts";

const AT = new Date("2026-01-02T03:04:00Z");

function runContext(): RunContext {
    return { signal: new AbortController().signal, env: {}, params: {} };
}

function setup(fn: TaskFn, settings: Partial<TaskSettings> = {}) {
    const calls: string[] = [];
    const logged: string[] = [];
    const log: Logger = {
        info: (m) => logged.push(`info ${m}`),
        warn: (m) => logged.push(`warn ${m}`),
        error: (m) => logged.push(`error ${m}`),
    };
    const control: TaskControl = {
        setPaused: (name, paused) => calls.push(`${name}:${paused ? "pause" : "resume"}`),
        setHeld: (name, held) => calls.push(`${name}:${held ? "ref" : "unref"}`),
        remove: (name) => calls.push(`${name}:remove`),
        nextRun: () => new Date(Date.now() + 60_000),
        nextRuns: (pattern, timezone, count) => {
            calls.push(`nextRuns ${pattern} ${String(timezone)} ${String(count)}`);
            return [AT];
        },
        match: (_pattern, _timezone, date) => date.getTime() === AT.getTime(),
        log: () => log,
    };
    const { task, internals } = createScheduledTask(control, {
        id: "nightly",
        name: "Nightly",
        pattern: "0 3 * * *",
        timezone: undefined,
        fn,
        maxExecutions: undefined,
        ...settings,
    });
    const events: string[] = [];
    for (const event of [
        "task:started",
        "task:stopped",
        "task:destroyed",
        "execution:failed",
        "execution:maxReached",
    ] as const) {
        task.on(event, () => events.push(event));
    }
    return { task, internals, calls, events, logged };
}

describe("ScheduledTask", () => {
    it("maps start, stop and destroy onto RunWisp pause, resume and removal", () => {
        const { task, calls, events } = setup(() => undefined);
        expect(task.getStatus()).toBe("stopped");
        expect(task.getNextRun()).toBeNull();
        task.stop();
        task.start();
        task.start();
        expect(task.getStatus()).toBe("idle");
        expect(task.getNextRun()).not.toBeNull();
        task.stop();
        task.destroy();
        task.start();
        expect(task.getStatus()).toBe("destroyed");
        expect(calls).toEqual(["nightly:resume", "nightly:pause", "nightly:remove"]);
        // As node-cron: start only from stopped, stop only from started.
        expect(events).toEqual(["task:started", "task:stopped", "task:destroyed"]);
    });

    it("stops before it is destroyed while started", () => {
        const { task, events } = setup(() => undefined);
        task.start();
        task.destroy();
        expect(events).toEqual(["task:started", "task:stopped", "task:destroyed"]);
    });

    it("passes ref and unref on to RunWisp until it is destroyed", () => {
        const { task, calls } = setup(() => undefined);
        task.unref();
        task.ref();
        task.destroy();
        task.unref();
        expect(calls).toEqual(["nightly:unref", "nightly:ref", "nightly:remove"]);
    });

    it("answers getNextRuns and match from the schedule, whatever the status", () => {
        const { task, calls } = setup(() => undefined, { timezone: "Europe/Bratislava" });
        expect(task.getNextRuns(3)).toEqual([AT]);
        expect(calls).toContain("nextRuns 0 3 * * * Europe/Bratislava 3");
        expect(task.match(AT)).toBe(true);
        expect(task.match(new Date(0))).toBe(false);
    });

    it("has no schedule to answer for without a pattern", () => {
        const { task } = setup(() => undefined, { pattern: "" });
        task.start();
        expect(task.getNextRun()).toBeNull();
        expect(task.msToNext()).toBeNull();
        expect(task.getNextRuns(2)).toEqual([]);
        expect(task.match(AT)).toBe(false);
    });

    it("records results and rethrows failures", async () => {
        const { task, internals, events } = setup((ctx) => {
            if (ctx.execution.reason === "invoked") return 7;
            throw new Error("boom");
        });
        expect(await task.execute()).toBe(7);
        expect(task.lastRun()?.result).toBe(7);
        await expect(internals.run(AT, runContext())).rejects.toThrow("boom");
        expect(task.lastRun()?.error?.message).toBe("boom");
        expect(events).toContain("execution:failed");
        expect(task.isBusy()).toBe(false);
    });

    it("dates a scheduled run by its firing, and counts only scheduled runs as running", async () => {
        let release: () => void = () => undefined;
        const contexts: { date: Date; reason: string; dateLocalIso: string }[] = [];
        const { task, internals } = setup(
            (ctx) => {
                contexts.push({
                    date: ctx.date,
                    reason: ctx.execution.reason,
                    dateLocalIso: ctx.dateLocalIso,
                });
                return new Promise((resolve) => {
                    release = () => {
                        resolve(undefined);
                    };
                });
            },
            { timezone: "Europe/Bratislava" },
        );
        task.start();
        const invoked = task.execute();
        expect(task.getStatus()).toBe("idle");
        release();
        await invoked;
        const scheduled = internals.run(AT, runContext());
        expect(task.getStatus()).toBe("running");
        expect(task.isBusy()).toBe(true);
        release();
        await scheduled;
        expect(task.getStatus()).toBe("idle");
        expect(contexts[0]?.reason).toBe("invoked");
        expect(contexts[1]).toEqual({
            date: AT,
            reason: "scheduled",
            dateLocalIso: "2026-01-02T04:04:00.000+01:00",
        });
    });

    it("destroys itself once a scheduled run succeeds with no runs left, as node-cron", async () => {
        let fail = false;
        const { task, internals, calls, events } = setup(
            () => {
                if (fail) throw new Error("boom");
            },
            { maxExecutions: 2 },
        );
        task.start();
        await task.execute();
        expect(task.runsLeft()).toBe(2);
        await internals.run(AT, runContext());
        expect(task.runsLeft()).toBe(1);
        fail = true;
        await expect(internals.run(AT, runContext())).rejects.toThrow("boom");
        // A failed last run doesn't reach the limit check.
        expect(task.runsLeft()).toBe(0);
        expect(task.getStatus()).toBe("idle");
        fail = false;
        await internals.run(AT, runContext());
        expect(task.getStatus()).toBe("destroyed");
        expect(calls).toContain("nightly:remove");
        expect(events).toContain("execution:maxReached");
    });

    it("logs a listener that throws or rejects, without failing the run", async () => {
        const { task, logged } = setup(() => 1);
        task.on("execution:started", () => {
            throw new Error("sync");
        });
        task.on("execution:finished", () => Promise.reject(new Error("async")));
        expect(await task.execute()).toBe(1);
        await new Promise((resolve) => setImmediate(resolve));
        expect(logged.some((l) => l.startsWith("error") && l.includes("sync"))).toBe(true);
        expect(logged.some((l) => l.startsWith("error") && l.includes("async"))).toBe(true);
    });

    it("calls a once() listener once", async () => {
        const { task } = setup(() => undefined);
        let calls = 0;
        task.once("execution:finished", () => calls++);
        await task.execute();
        await task.execute();
        expect(calls).toBe(1);
    });

    it("writes to its own logger over the instance's", () => {
        const own: string[] = [];
        const { internals, logged } = setup(() => undefined, {
            logger: { info: () => undefined, warn: (m) => own.push(m), error: () => undefined },
        });
        internals.missed(AT);
        expect(own).toHaveLength(1);
        expect(logged).toEqual([]);
    });
});

describe("ScheduledTask internals", () => {
    it("keeps them off the handle and out of the package's values", async () => {
        const { task } = setup(() => undefined);
        for (const internal of ["run", "retire", "overlap", "missed", "control", "fn"]) {
            expect(internal in task).toBe(false);
        }
        const sdk = await import("./index.ts");
        expect("ScheduledTask" in sdk).toBe(false);
    });

    it("emits node-cron's execution:overlap for the skipped firing", () => {
        const { task, internals } = setup(() => undefined);
        const dates: Date[] = [];
        task.on("execution:overlap", (ctx) => dates.push(ctx.date));
        internals.overlap(AT);
        expect(dates).toEqual([AT]);
    });

    it("emits execution:overlap for a firing that comes while a scheduled run is going", async () => {
        const pending: (() => void)[] = [];
        const { task, internals } = setup(
            () =>
                new Promise((resolve) => {
                    pending.push(() => {
                        resolve(undefined);
                    });
                }),
        );
        const dates: Date[] = [];
        task.on("execution:overlap", (ctx) => dates.push(ctx.date));
        const first = internals.run(AT, runContext());
        const later = new Date(AT.getTime() + 60_000);
        const second = internals.run(later, runContext());
        for (const release of pending) release();
        await Promise.all([first, second]);
        expect(dates).toEqual([later]);
    });

    it("emits execution:missed, warning only when nothing listens", () => {
        const { task, internals, logged } = setup(() => undefined, { timezone: "UTC" });
        internals.missed(AT);
        expect(logged).toEqual([
            'warn runwisp: task "Nightly" missed its run at 2026-01-02T03:04:00.000Z: RunWisp was not running',
        ]);
        const dates: Date[] = [];
        task.on("execution:missed", (ctx) => dates.push(ctx.date));
        internals.missed(AT);
        expect(dates).toEqual([AT]);
        expect(logged).toHaveLength(1);
    });

    it("keeps quiet about a missed run with suppressMissedWarning", () => {
        const { internals, logged } = setup(() => undefined, { suppressMissedWarning: true });
        internals.missed(AT);
        expect(logged).toEqual([]);
    });

    it("retires without touching RunWisp's config", () => {
        const { task, internals, calls, events } = setup(() => undefined);
        task.start();
        calls.length = 0;
        internals.retire();
        internals.retire();
        expect(task.getStatus()).toBe("destroyed");
        expect(calls).toEqual([]);
        expect(events).toEqual(["task:started", "task:stopped", "task:destroyed"]);
    });
});

describe("localIso", () => {
    it("is local time in the zone with its offset, Z at UTC", () => {
        expect(localIso(AT, "UTC")).toBe("2026-01-02T03:04:00.000Z");
        expect(localIso(new Date("2026-07-01T00:00:00.123Z"), "America/New_York")).toBe(
            "2026-06-30T20:00:00.123-04:00",
        );
    });
});
