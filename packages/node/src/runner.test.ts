// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import { Runner, type OverlapPolicy, type Reporter, type Serve } from "./runner.ts";
import type { RunRequest } from "./session.ts";

/** A session that records what the runner reports, and resolves exited(run) with the exit code. */
function reporter() {
    const lines: string[] = [];
    const reported: string[] = [];
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
        exit: (run, code) => {
            reported.push(run);
            void exited(run);
            exits.get(run)?.(code);
        },
    };
    return { session, lines, reported, exited };
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
            sink("stdout", `running ${req.run}`);
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
        const { session, lines, exited } = reporter();
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
