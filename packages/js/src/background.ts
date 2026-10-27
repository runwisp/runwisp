// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// node-cron's background tasks: `schedule(expression, "./job.js")` runs the
// file's exported `task` function in a forked Node process.

import { spawn } from "node:child_process";
import { dirname, extname, isAbsolute, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

import { field } from "./daemon.ts";
import type { TaskContext } from "./scheduled-task.ts";

/**
 * On a run's context: an AbortSignal that aborts once RunWisp gave up on the
 * stopped run after its graceful_stop. A task file's process is killed then.
 */
export const KILL: unique symbol = Symbol("runwisp.kill");

const HERE = dirname(fileURLToPath(import.meta.url));
const EXT = extname(import.meta.url);

/** Stack frames of this package's own code that sits between the caller and solvePath. */
const OWN_FRAMES = [`${HERE}/background${EXT}:`, `${HERE}/runwisp${EXT}:`];

/**
 * node-cron's solvePath: filePath as a file URL. A relative path resolves
 * against the directory of the file that called (schedule, createTask or
 * solvePath itself), not the working directory.
 */
export function solvePath(filePath: string): string {
    if (isAbsolute(filePath)) return pathToFileURL(filePath).href;
    if (filePath.startsWith("file://")) return filePath;
    const caller = new Error().stack
        ?.split("\n")
        .slice(1)
        .find((line) => !OWN_FRAMES.some((own) => line.includes(own)));
    const match = caller ? /(file:\/\/)?(\/.+):\d+:\d+/.exec(caller) : null;
    const file = match?.[2];
    if (file === undefined) throw new Error(`Could not locate task file ${filePath}`);
    const path = match?.[1] ? fileURLToPath(`file://${file}`) : file;
    return pathToFileURL(resolve(dirname(path), filePath)).href;
}

// The forked process: loads the file, runs its `task` with the context, sends
// back the result or the error, and exits even when the file left handles
// open (a database pool, say), once its output had a second to drain. It
// leads its own process group: when the app dies (its IPC channel closes) it
// kills the group, the processes it started included.
const CHILD = `
const orphaned = () => process.kill(-process.pid, "SIGKILL");
process.once("disconnect", orphaned);
process.once("message", ({ path, context }) => {
    const done = (reply, code) => {
        process.exitCode = code;
        process.off("disconnect", orphaned);
        process.send(reply, () => {
            process.disconnect();
            setTimeout(() => process.exit(), 1000).unref();
        });
    };
    import(path)
        .then((mod) => {
            if (typeof mod.task !== "function") throw new Error(path + " does not export a task function");
            return mod.task({
                ...context,
                date: new Date(context.date),
                triggeredAt: new Date(context.triggeredAt),
                execution: { ...context.execution, startedAt: new Date(context.execution.startedAt) },
                signal: new AbortController().signal,
            });
        })
        .then(
            (result) => done({ result }, 0),
            (err) => done({ error: { name: err?.name, message: err?.message ?? String(err), stack: err?.stack } }, 1),
        );
});
`;

/** The error the forked process sent, as an Error with its name, message and stack. */
function revive(sent: unknown): Error {
    const error = new Error(String(field(sent, "message")));
    const name = field(sent, "name");
    const stack = field(sent, "stack");
    if (typeof name === "string") error.name = name;
    if (typeof stack === "string") error.stack = stack;
    return error;
}

/** Signals the process group child leads, unless it is gone already. */
function signalGroup(pid: number | undefined, signal: NodeJS.Signals): void {
    if (pid === undefined) return;
    try {
        process.kill(-pid, signal);
    } catch {
        // The group has exited.
    }
}

/**
 * A callback that runs node-cron background task file url (a module that
 * exports `task`) in a forked Node process, one per run. Its output is the
 * run's output, its error or exit fails the run, and stopping the run sends
 * SIGTERM to the process and the ones it started; the context's KILL signal
 * sends them SIGKILL. The run's env is added to the process env.
 */
export function forkedTask(
    url: string,
): (context: Omit<TaskContext, "task" | "error">) => Promise<unknown> {
    return (context) =>
        new Promise((resolvePromise, reject) => {
            const child = spawn(process.execPath, [...process.execArgv, "-e", CHILD], {
                stdio: ["ignore", "pipe", "pipe", "ipc"],
                env: { ...process.env, ...context.env },
                // Its own process group, so a stop reaches what it started too.
                detached: true,
            });
            child.stdout?.on("data", (chunk: Buffer) => process.stdout.write(chunk));
            child.stderr?.on("data", (chunk: Buffer) => process.stderr.write(chunk));
            let reply: unknown;
            child.on("message", (message) => (reply = message));
            const stop = () => {
                signalGroup(child.pid, "SIGTERM");
            };
            const kill = () => {
                signalGroup(child.pid, "SIGKILL");
            };
            const killed: unknown = Reflect.get(context, KILL);
            const killSignal = killed instanceof AbortSignal ? killed : undefined;
            context.signal.addEventListener("abort", stop, { once: true });
            killSignal?.addEventListener("abort", kill, { once: true });
            child.once("error", reject);
            child.once("close", (code, signal) => {
                context.signal.removeEventListener("abort", stop);
                killSignal?.removeEventListener("abort", kill);
                const error = field(reply, "error");
                if (error !== undefined) reject(revive(error));
                else if (code === 0 && reply !== undefined) resolvePromise(field(reply, "result"));
                else
                    reject(
                        new Error(
                            `the background task ${url} exited with ${signal ?? `code ${String(code)}`}`,
                        ),
                    );
            });
            const { date, dateLocalIso, triggeredAt, execution, env, params, runId } = context;
            child.send({
                path: url,
                context: { date, dateLocalIso, triggeredAt, execution, env, params, runId },
            });
        });
}
