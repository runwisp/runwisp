// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { afterAll, describe, expect, it } from "vitest";

import { forkedTask, solvePath } from "./background.ts";
import { runCaptured } from "./capture.ts";
import type { Stream } from "./session.ts";
import type { TaskContext } from "./scheduled-task.ts";

const dir = mkdtempSync(join(tmpdir(), "rwn-bg-"));
afterAll(() => {
    rmSync(dir, { recursive: true, force: true });
});

function job(name: string, code: string): string {
    const path = join(dir, name);
    writeFileSync(path, code);
    return pathToFileURL(path).href;
}

function context(signal = new AbortController().signal): Omit<TaskContext, "task"> {
    const date = new Date("2026-01-02T03:04:00Z");
    return {
        signal,
        env: { GREETING: "hi" },
        params: { who: "you" },
        date,
        dateLocalIso: "2026-01-02T03:04:00.000Z",
        triggeredAt: date,
        execution: { id: "01JRUN", reason: "scheduled", startedAt: date },
        runId: "01JRUN",
    };
}

async function captured(url: string, ctx = context()) {
    const lines: [Stream, string][] = [];
    let result: unknown;
    let error: Error | undefined;
    try {
        result = await runCaptured(
            (stream, line) => {
                lines.push([stream, line]);
            },
            () => forkedTask(url)(ctx),
        );
    } catch (err) {
        if (!(err instanceof Error)) throw err;
        error = err;
    }
    return { lines, result, error };
}

describe("solvePath", () => {
    it("resolves a relative path against the calling file, as node-cron", () => {
        const here = dirname(fileURLToPath(import.meta.url));
        expect(solvePath("./jobs/a.js")).toBe(pathToFileURL(join(here, "jobs/a.js")).href);
        expect(solvePath("/srv/a.js")).toBe("file:///srv/a.js");
        expect(solvePath("file:///srv/a.js")).toBe("file:///srv/a.js");
    });
});

describe("forkedTask", () => {
    it("runs the module's task in a new process, its output the run's", async () => {
        const url = job(
            "ok.mjs",
            `export async function task(ctx) {
                console.log(process.env.GREETING, ctx.params.who, ctx.date.toISOString(), ctx.execution.reason, ctx.runId, ctx.execution.id);
                console.error("to stderr");
                return { pid: process.pid };
            }`,
        );
        const { lines, result, error } = await captured(url);
        expect(lines).toEqual([
            ["stdout", "hi you 2026-01-02T03:04:00.000Z scheduled 01JRUN 01JRUN"],
            ["stderr", "to stderr"],
        ]);
        expect(error).toBeUndefined();
        // Another process ran it.
        const pid: unknown = Reflect.get(Object(result), "pid");
        expect(typeof pid).toBe("number");
        expect(pid).not.toBe(process.pid);
    });

    it("fails the run with the error the task threw", async () => {
        const url = job(
            "throws.mjs",
            `export function task() { throw new TypeError("bad input"); }`,
        );
        const { error } = await captured(url);
        expect(error?.name).toBe("TypeError");
        expect(error?.message).toBe("bad input");
    });

    it("fails the run when the module has no task, or the process dies", async () => {
        const none = await captured(job("none.mjs", `export const x = 1;`));
        expect(none.error?.message).toContain("does not export a task function");
        const exits = await captured(
            job("exits.mjs", `export function task() { process.exit(3); }`),
        );
        expect(exits.error?.message).toContain("exited with code 3");
    });

    it("stops the process when the run is stopped", async () => {
        const url = job(
            "hangs.mjs",
            `export function task() { return new Promise(() => setInterval(() => {}, 1000)); }`,
        );
        const abort = new AbortController();
        const pending = captured(url, context(abort.signal));
        setTimeout(() => {
            abort.abort();
        }, 300);
        const { error } = await pending;
        expect(error?.message).toContain("exited with SIGTERM");
    });
});
