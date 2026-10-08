// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import { ScheduledTask, type TaskControl, type TaskFn } from "./scheduled-task.ts";

function setup(fn: TaskFn, maxExecutions?: number) {
    const calls: string[] = [];
    const control: TaskControl = {
        setPaused: (name, paused) => calls.push(`${name}:${paused ? "pause" : "resume"}`),
        remove: (name) => calls.push(`${name}:remove`),
        nextRun: () => new Date(0),
    };
    const task = new ScheduledTask(control, "nightly", "Nightly", "0 3 * * *", fn, maxExecutions);
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
    return { task, calls, events };
}

describe("ScheduledTask", () => {
    it("maps start, stop and destroy onto RunWisp pause, resume and removal", () => {
        const { task, calls, events } = setup(() => undefined);
        expect(task.getStatus()).toBe("stopped");
        expect(task.getNextRun()).toBeNull();
        task.start();
        expect(task.getStatus()).toBe("idle");
        expect(task.getNextRun()).toEqual(new Date(0));
        task.stop();
        task.destroy();
        task.start();
        expect(task.getStatus()).toBe("destroyed");
        expect(calls).toEqual(["nightly:resume", "nightly:pause", "nightly:remove"]);
        expect(events).toEqual(["task:started", "task:stopped", "task:destroyed"]);
    });

    it("records results and rethrows failures", async () => {
        const { task, events } = setup((ctx) => {
            if (ctx.execution.reason === "invoked") return 7;
            throw new Error("boom");
        });
        expect(await task.execute()).toBe(7);
        expect(task.lastRun()?.result).toBe(7);
        await expect(
            task.run("scheduled", { signal: new AbortController().signal, env: {}, params: {} }),
        ).rejects.toThrow("boom");
        expect(task.lastRun()?.error?.message).toBe("boom");
        expect(events).toContain("execution:failed");
        expect(task.isBusy()).toBe(false);
    });

    it("destroys itself after maxExecutions", async () => {
        const { task, calls, events } = setup(() => undefined, 2);
        task.start();
        await task.execute();
        expect(task.runsLeft()).toBe(1);
        await task.execute();
        expect(task.runsLeft()).toBe(0);
        expect(task.getStatus()).toBe("destroyed");
        expect(calls).toContain("nightly:remove");
        expect(events).toContain("execution:maxReached");
    });
});
