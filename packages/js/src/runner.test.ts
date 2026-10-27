// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import { Runner, type OverlapPolicy, type Reporter, type Serve } from "./runner.ts";
import type { RunRequest } from "./session.ts";

/** A session that records what the runner reports, and resolves exited(run) with the exit code. */
function reporter() {
    const lines: string[] = [];
    const reported: string[] = [];
    const reasons = new Map<string, string>();
    const exits = new Map<string, (code: number) => void>();
    const codes = new Map<string, Promise<number>>();
    const exited = (run: string): Promise<number> => {
        let promise = codes.get(run);
        if (!promise) {
            promise = new Promise((resolve) => exits.set(run, resolve));
            codes.set(run, promise);
        }
        return promise;
    };
    const session: Reporter = {
        output: (_run, stream, line) => lines.push(`${stream}: ${line}`),
        exit: (run, code, reason) => {
            reported.push(run);
            if (reason) reasons.set(run, reason);
            void exited(run);
            exits.get(run)?.(code);
        },
        flush: () => undefined,
    };
    return { session, lines, reported, reasons, exited };
}

const request = (run: string): RunRequest => ({ run, task: "job", env: {}, params: {} });

function deferred(): { promise: Promise<number>; resolve: (code: number) => void } {
    let resolve: (code: number) => void = () => undefined;
    const promise = new Promise<number>((r) => (resolve = r));
    return { promise, resolve };
}

describe("Runner", () => {
    it("reports output and the exit code, and 143 for a stopped run", async () => {
        const { session, lines, exited } = reporter();
        const serve: Serve = (req, signal, sink) => {
            sink("stdout", `running ${req.run}`, false);
            if (req.run === "ok") return Promise.resolve(0);
            return new Promise((resolve) => {
                signal.addEventListener("abort", () => {
                    resolve(0);
                });
            });
        };
        const runner = new Runner(serve, () => ({ onOverlap: "queue", maxConcurrent: 2 }));
        runner.start(session, request("ok"));
        expect(await exited("ok")).toBe(0);

        runner.start(session, request("slow"));
        runner.stop("slow");
        expect(await exited("slow")).toBe(143);
        expect(lines).toEqual(["stdout: running ok", "stdout: running slow"]);
    });

    it("keeps a callback that ignored its stop from overlapping the next run", async () => {
        const { session, lines, reasons, exited } = reporter();
        const stubborn = deferred();
        let calls = 0;
        const policy: OverlapPolicy = { onOverlap: "skip", maxConcurrent: 1 };
        const runner = new Runner(
            () => (++calls === 1 ? stubborn.promise : Promise.resolve(0)),
            () => policy,
        );

        // RunWisp gave up on the first run after graceful_stop; the callback goes on.
        runner.start(session, request("first"));
        runner.stop("first");

        runner.start(session, request("skipped"));
        expect(await exited("skipped")).toBe(1);
        expect(reasons.get("skipped")).toBe("skipped");
        expect(lines).toContain(
            "stderr: runwisp: skipped: the previous run is still running in the app after RunWisp stopped it",
        );

        policy.onOverlap = "queue";
        runner.start(session, request("queued"));
        await Promise.resolve();
        expect(lines).toContain(
            "stderr: runwisp: waiting for the previous run, which ignored its stop signal, to finish",
        );
        stubborn.resolve(0);
        expect(await exited("queued")).toBe(0);
        expect(calls).toBe(2);
    });

    it("doesn't start a queued run once the runner is closing", async () => {
        const { session, exited } = reporter();
        const stubborn = deferred();
        let calls = 0;
        const runner = new Runner(
            () => (++calls === 1 ? stubborn.promise : Promise.resolve(0)),
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("first"));
        runner.stop("first");
        runner.start(session, request("queued"));
        await Promise.resolve();

        const closed = runner.close();
        stubborn.resolve(0);
        await closed;
        expect(await exited("queued")).toBe(1);
        expect(calls).toBe(1);
    });

    it("stops a queued run, which isn't running yet, instead of starting it later", async () => {
        const { session, lines, exited } = reporter();
        const stubborn = deferred();
        let calls = 0;
        const runner = new Runner(
            () => (++calls === 1 ? stubborn.promise : Promise.resolve(0)),
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("first"));
        runner.stop("first");
        runner.start(session, request("queued"));
        await Promise.resolve();

        runner.stop("queued");
        expect(await exited("queued")).toBe(143);
        expect(lines).toContain(
            "stderr: runwisp: this run never started: the previous run ignored its stop signal and still runs",
        );
        stubborn.resolve(0);
        await runner.close();
        expect(calls).toBe(1);
    });

    it("stops the queued runs of a session that disconnected", async () => {
        const { session, exited } = reporter();
        const stubborn = deferred();
        let calls = 0;
        const runner = new Runner(
            () => (++calls === 1 ? stubborn.promise : Promise.resolve(0)),
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("first"));
        runner.stop("first");
        runner.start(session, request("queued"));
        await Promise.resolve();

        runner.disconnected(session);
        expect(await exited("queued")).toBe(143);
        stubborn.resolve(0);
        await runner.close();
        expect(calls).toBe(1);
    });

    it("writes an app crash into the runs it takes down", async () => {
        const { session, lines, exited } = reporter();
        const doomed = deferred();
        const runner = new Runner(
            () => doomed.promise,
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("doomed"));
        await Promise.resolve();
        runner.reportCrash(new Error("kaboom"));
        doomed.resolve(1);
        await exited("doomed");
        expect(lines).toContain("stderr: runwisp: the app crashed while this run was in progress:");
        expect(lines.some((line) => line.includes("Error: kaboom"))).toBe(true);
    });

    it("aborts the kill signal of a run RunWisp gave up on, after its stop signal", async () => {
        const { session, exited } = reporter();
        const seen: string[] = [];
        const runner = new Runner(
            (_req, signal, _sink, kill) =>
                new Promise((resolve) => {
                    signal.addEventListener("abort", () => seen.push("stop"));
                    kill.addEventListener("abort", () => {
                        seen.push("kill");
                        resolve(137);
                    });
                }),
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("r1"));
        await Promise.resolve();
        expect(runner.size).toBe(1);
        runner.kill("r1");
        expect(await exited("r1")).toBe(143);
        expect(seen).toEqual(["stop", "kill"]);
        expect(runner.size).toBe(0);
    });

    it("fails the run, instead of crashing the app, when serve itself rejects", async () => {
        const { session, lines, exited } = reporter();
        const runner = new Runner(
            () => Promise.reject(new Error("serve broke")),
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("r1"));
        expect(await exited("r1")).toBe(1);
        expect(lines.some((line) => line.startsWith("stderr: runwisp: Error: serve broke"))).toBe(
            true,
        );
    });

    it("writes a crash whose error can't be printed into the runs it takes down", async () => {
        const { session, lines, exited } = reporter();
        const doomed = deferred();
        const runner = new Runner(
            () => doomed.promise,
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("doomed"));
        await Promise.resolve();
        const hostile = new Proxy(
            {},
            {
                get() {
                    throw new Error("no");
                },
                getPrototypeOf() {
                    throw new Error("no");
                },
            },
        );
        runner.reportCrash(hostile);
        doomed.resolve(1);
        await exited("doomed");
        expect(lines).toContain("stderr: runwisp: the app crashed while this run was in progress:");
        expect(lines.some((line) => line.includes("can't print the thrown value"))).toBe(true);
    });

    it("on close, takes no new runs and waits for running ones", async () => {
        const { session, lines, reported, exited } = reporter();
        const running = deferred();
        const runner = new Runner(
            () => running.promise,
            () => ({ onOverlap: "queue", maxConcurrent: 1 }),
        );
        runner.start(session, request("running"));
        await Promise.resolve();

        const closed = runner.close();
        runner.start(session, request("late"));
        expect(await exited("late")).toBe(1);
        expect(lines).toContain("stderr: runwisp: the app is shutting down");

        running.resolve(0);
        await closed;
        // Reported before close() resolved, so it reaches RunWisp before the app disconnects.
        expect(reported).toContain("running");
    });
});
