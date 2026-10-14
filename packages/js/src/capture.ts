// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { AsyncLocalStorage } from "node:async_hooks";
import { dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { StringDecoder } from "node:string_decoder";
import { format } from "node:util";

import type { Stream } from "./session.ts";

/** Gets a run's output: a line, or with partial a piece of one that the next call continues. */
export type LineSink = (stream: Stream, line: string, partial: boolean) => void;

/**
 * What the patched console and streams see of the run in progress. Every
 * copy of this package loaded in the process shares the patch and the store,
 * so this shape is a contract between copies: keep it stable.
 */
interface CaptureTarget {
    push(stream: Stream, text: string): void;
    /** Inside a patched console call, which reports its own lines: its write to the stream is skipped. */
    inConsole: boolean;
}

/**
 * How much of a line is held before it goes out as a partial piece, in UTF-16
 * units: the daemon splits log lines at 64 KiB, and one message must stay far
 * below its 16 MiB cap. Also bounds `\r` progress output, which never ends a line.
 */
export const MAX_PIECE = 64 * 1024;

const PATCHED = Symbol.for("runwisp.outputCapture");
const STORE = Symbol.for("runwisp.outputCapture.store.v1");

function isStore(value: unknown): value is AsyncLocalStorage<CaptureTarget> {
    return value instanceof AsyncLocalStorage;
}

/** One store per process, on globalThis: a second copy of this package must feed the same patch. */
function store(): AsyncLocalStorage<CaptureTarget> {
    const found: unknown = Reflect.get(globalThis, STORE);
    if (isStore(found)) return found;
    const created = new AsyncLocalStorage<CaptureTarget>();
    Object.defineProperty(globalThis, STORE, { value: created });
    return created;
}

const METHODS = [
    ["log", "stdout"],
    ["info", "stdout"],
    ["debug", "stdout"],
    ["warn", "stderr"],
    ["error", "stderr"],
] as const;

/** Where a piece may end: not between the halves of a surrogate pair. */
function pieceEnd(text: string): number {
    const code = text.charCodeAt(MAX_PIECE - 1);
    return code >= 0xd800 && code <= 0xdbff ? MAX_PIECE - 1 : MAX_PIECE;
}

/** Sends text as partial pieces while it is longer than MAX_PIECE, and returns what is left. */
function sendPieces(sink: LineSink, stream: Stream, text: string): string {
    let rest = text;
    while (rest.length > MAX_PIECE) {
        const end = pieceEnd(rest);
        sink(stream, rest.slice(0, end), true);
        rest = rest.slice(end);
    }
    return rest;
}

/** One run's capture: what it has written without a newline yet. */
export class Capture implements CaptureTarget {
    inConsole = false;
    private readonly sink: LineSink;
    private readonly partial: Record<Stream, string> = { stdout: "", stderr: "" };

    constructor(sink: LineSink) {
        this.sink = sink;
    }

    push(stream: Stream, text: string): void {
        const lines = (this.partial[stream] + text).split("\n");
        const rest = lines.pop() ?? "";
        for (const line of lines) this.sink(stream, sendPieces(this.sink, stream, line), false);
        this.partial[stream] = sendPieces(this.sink, stream, rest);
    }

    /** A last line without a newline still belongs to the run. */
    flush(): void {
        for (const stream of ["stdout", "stderr"] as const) {
            if (this.partial[stream] !== "") this.sink(stream, this.partial[stream], false);
            this.partial[stream] = "";
        }
    }
}

/**
 * Patches console and process.stdout/stderr once per process, so that output
 * written while a job runs is also sent to that job's sink: console.* and
 * loggers that write to the streams directly (Nest's ConsoleLogger, for one).
 * Output always reaches the real console too. Console is patched as well as
 * the streams because test runners and frameworks replace the global console
 * with one that never writes to process.stdout.
 */
function installOutputCapture(captures: AsyncLocalStorage<CaptureTarget>): void {
    if (Object.hasOwn(console, PATCHED)) return;
    Object.defineProperty(console, PATCHED, { value: true });
    for (const [method, stream] of METHODS) {
        const original = console[method].bind(console);
        console[method] = (...args: unknown[]) => {
            const capture = captures.getStore();
            if (!capture || capture.inConsole) {
                original(...args);
                return;
            }
            capture.inConsole = true;
            try {
                original(...args);
            } finally {
                capture.inConsole = false;
            }
            capture.push(stream, `${format(...args)}\n`);
        };
    }
    for (const [stream, out] of [
        ["stdout", process.stdout],
        ["stderr", process.stderr],
    ] as const) {
        const original = out.write.bind(out);
        // A multibyte character can be split between two chunks of one stream.
        const decoders = new WeakMap<CaptureTarget, StringDecoder>();
        const write = (chunk: unknown, ...rest: unknown[]): boolean => {
            const capture = captures.getStore();
            if (capture && !capture.inConsole) {
                const encoding = typeof rest[0] === "string" ? rest[0] : undefined;
                let decoder = decoders.get(capture);
                if (!decoder) {
                    decoder = new StringDecoder();
                    decoders.set(capture, decoder);
                }
                capture.push(stream, chunkText(chunk, encoding, decoder));
            }
            const flushed: unknown = Reflect.apply(original, out, [chunk, ...rest]);
            return flushed === true;
        };
        // The patched write has the original's overloads.
        Object.defineProperty(out, "write", { value: write, writable: true, configurable: true });
    }
}

function chunkText(chunk: unknown, encoding: string | undefined, decoder: StringDecoder): string {
    if (typeof chunk === "string") {
        return encoding !== undefined && Buffer.isEncoding(encoding)
            ? Buffer.from(chunk, encoding).toString()
            : chunk;
    }
    if (chunk instanceof Uint8Array) return decoder.write(Buffer.from(chunk));
    return String(chunk);
}

/** Runs fn with output written inside it (and inside anything it awaits) copied to sink, line by line. */
export async function runCaptured<T>(sink: LineSink, fn: () => Promise<T>): Promise<T> {
    const captures = store();
    installOutputCapture(captures);
    const capture = new Capture(sink);
    try {
        return await captures.run(capture, fn);
    } finally {
        capture.flush();
    }
}

// Not new URL(".", import.meta.url): webpack rewrites that into an asset import.
const PACKAGE_DIR = `${dirname(fileURLToPath(import.meta.url))}/`;

/** The error's stack cut at the first frame of this package: everything below is plumbing. */
export function ownStack(err: unknown): unknown {
    if (!(err instanceof Error) || err.stack === undefined) return err;
    const lines = err.stack.split("\n");
    const cut = lines.findIndex((line) => line.includes(PACKAGE_DIR));
    return (cut === -1 ? lines : lines.slice(0, cut)).join("\n");
}
