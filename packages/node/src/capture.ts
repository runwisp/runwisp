// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { AsyncLocalStorage } from "node:async_hooks";
import { fileURLToPath } from "node:url";
import { format } from "node:util";

import type { Stream } from "./session.ts";

export type LineSink = (stream: Stream, line: string) => void;

const sinks = new AsyncLocalStorage<LineSink>();
const PATCHED = Symbol.for("runwisp.consoleCapture");

const METHODS = [
    ["log", "stdout"],
    ["info", "stdout"],
    ["debug", "stdout"],
    ["warn", "stderr"],
    ["error", "stderr"],
] as const;

/**
 * Patches console once per process so that output written while a job runs is
 * also sent to that job's sink. Output always reaches the real console too.
 */
function installConsoleCapture(): void {
    if (Object.hasOwn(console, PATCHED)) return;
    Object.defineProperty(console, PATCHED, { value: true });
    for (const [method, stream] of METHODS) {
        const original = console[method].bind(console);
        console[method] = (...args: unknown[]) => {
            original(...args);
            const sink = sinks.getStore();
            if (!sink) return;
            for (const line of format(...args).split("\n")) sink(stream, line);
        };
    }
}

/** Runs fn with console output inside it (and inside anything it awaits) copied to sink. */
export function runCaptured<T>(sink: LineSink, fn: () => Promise<T>): Promise<T> {
    installConsoleCapture();
    return sinks.run(sink, fn);
}

const PACKAGE_DIR = fileURLToPath(new URL(".", import.meta.url));

/** The error's stack cut at the first frame of this package: everything below is plumbing. */
export function ownStack(err: unknown): unknown {
    if (!(err instanceof Error) || err.stack === undefined) return err;
    const lines = err.stack.split("\n");
    const cut = lines.findIndex((line) => line.includes(PACKAGE_DIR));
    return (cut === -1 ? lines : lines.slice(0, cut)).join("\n");
}
