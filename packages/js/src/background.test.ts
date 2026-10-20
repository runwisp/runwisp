// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { spawn } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { afterAll, describe, expect, it } from "vitest";

import { forkedTask, KILL, solvePath } from "./background.ts";
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

/** Whether a process is gone, waiting up to 3 s for it to go. */
async function gone(pid: number): Promise<boolean> {
    for (let i = 0; i < 60; i++) {
        try {
            process.kill(pid, 0);
        } catch {
            return true;
        }
        await new Promise((resolve) => setTimeout(resolve, 50));
    }
    return false;
}

/** The pid a job wrote to path, once it did. */
async function pidIn(path: string): Promise<number> {
    for (let i = 0; i < 100; i++) {
        try {
            const pid = Number(readFileSync(path, "utf8"));
            if (pid > 0) return pid;
        } catch {
            // Not written yet.
        }
        await new Promise((resolve) => setTimeout(resolve, 50));
    }
    throw new Error(`no pid in ${path}`);
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

    it("stops the processes the task started too", async () => {
        const pidFile = join(dir, "grandchild.pid");
        const url = job(
            "starts.mjs",
            `import { spawn } from "node:child_process";
            import { writeFileSync } from "node:fs";
            export function task() {
                const sleeper = spawn("sleep", ["30"], { stdio: "ignore" });
                writeFileSync(${JSON.stringify(pidFile)}, String(sleeper.pid));
                return new Promise(() => {});
            }`,
        );
        const abort = new AbortController();
        const pending = captured(url, context(abort.signal));
        const grandchild = await pidIn(pidFile);
        abort.abort();
        await pending;
        expect(await gone(grandchild)).toBe(true);
    });

    it("kills a process that ignores SIGTERM once the run's kill signal aborts", async () => {
        const url = job(
            "stubborn.mjs",
            `export function task() {
                process.on("SIGTERM", () => console.log("ignoring SIGTERM"));
                return new Promise(() => setInterval(() => {}, 1000));
            }`,
        );
        const abort = new AbortController();
        const kill = new AbortController();
        const ctx = { ...context(abort.signal), [KILL]: kill.signal };
        const pending = captured(url, ctx);
        setTimeout(() => {
            abort.abort();
        }, 300);
        setTimeout(() => {
            kill.abort();
        }, 600);
        const { lines, error } = await pending;
        expect(lines).toContainEqual(["stdout", "ignoring SIGTERM"]);
        expect(error?.message).toContain("exited with SIGKILL");
    });

    it("ends the process and what it started when the app dies", async () => {
        const pidFile = join(dir, "orphan.pid");
        const sleeperFile = join(dir, "orphan-sleeper.pid");
        const url = job(
            "orphan.mjs",
            `import { spawn } from "node:child_process";
            import { writeFileSync } from "node:fs";
            export function task() {
                writeFileSync(${JSON.stringify(sleeperFile)}, String(spawn("sleep", ["30"], { stdio: "ignore" }).pid));
                writeFileSync(${JSON.stringify(pidFile)}, String(process.pid));
                return new Promise(() => {});
            }`,
        );
        const background = pathToFileURL(
            join(dirname(fileURLToPath(import.meta.url)), "background.ts"),
        ).href;
        // The app: runs the task file, then is killed with SIGKILL.
        const app = spawn(
            process.execPath,
            [
                "--input-type=module",
                "-e",
                `const { forkedTask } = await import(${JSON.stringify(background)});
                const context = { ...${JSON.stringify(context())}, signal: new AbortController().signal };
                forkedTask(${JSON.stringify(url)})(context);`,
            ],
            { stdio: "ignore" },
        );
        const child = await pidIn(pidFile);
        const sleeper = await pidIn(sleeperFile);
        app.kill("SIGKILL");
        expect(await gone(child)).toBe(true);
        expect(await gone(sleeper)).toBe(true);
    });
});
