// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { spawn, type ChildProcess } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";

import { afterEach, beforeEach, describe, expect, it } from "vitest";

import { daemonPaths, healthy, stopDaemon } from "../src/daemon.ts";
import { binary, freePort, trigger } from "./helpers.ts";

const APP = fileURLToPath(new URL("./fixtures/app.ts", import.meta.url));

describe("copies of the app, crashes and stops", () => {
    let data: string;
    let socket: string;
    let port: number;
    const copies: ChildProcess[] = [];

    /** Starts a copy of the app and resolves once it is ready. */
    function startCopy(): Promise<ChildProcess> {
        const child = spawn(process.execPath, [APP, data, String(port), binary], {
            stdio: ["pipe", "pipe", "pipe"],
        });
        copies.push(child);
        // Some tests crash a copy on purpose: show its stderr only when it never got ready.
        const stderr: string[] = [];
        child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk.toString()));
        return new Promise((resolve, reject) => {
            createInterface({ input: child.stdout }).on("line", (line) => {
                if (line === "ready") resolve(child);
            });
            child.once("exit", (code) => {
                reject(
                    new Error(
                        `copy exited with ${String(code)} before it was ready\n${stderr.join("")}`,
                    ),
                );
            });
        });
    }

    function exited(child: ChildProcess): Promise<void> {
        return new Promise((resolve) => {
            if (child.exitCode !== null || child.signalCode !== null) resolve();
            else
                child.once("exit", () => {
                    resolve();
                });
        });
    }

    async function closeCopy(child: ChildProcess): Promise<void> {
        child.stdin?.write("close\n");
        await exited(child);
    }

    beforeEach(async () => {
        data = mkdtempSync(join(tmpdir(), "rwn-e2e-"));
        socket = daemonPaths(data).socket;
        port = await freePort();
    });

    afterEach(async () => {
        for (const child of copies.splice(0)) child.kill("SIGKILL");
        await stopDaemon(socket);
        rmSync(data, { recursive: true, force: true });
    });

    it("runs each job once, in one copy, and hands over when that copy dies", async () => {
        const first = await startCopy();
        const second = await startCopy();
        expect((await trigger(socket, "who")).log).toContain(`pid ${String(first.pid)}`);

        first.kill("SIGKILL");
        await exited(first);
        const run = await trigger(socket, "who");
        expect(run.exitCode).toBe(0);
        expect(run.log).toContain(`pid ${String(second.pid)}`);
    });

    it("keeps RunWisp running while any copy is left, and stops it with the last", async () => {
        const first = await startCopy();
        const second = await startCopy();
        await closeCopy(first);
        expect(await healthy(socket)).toBe(true);
        expect((await trigger(socket, "who")).log).toContain(`pid ${String(second.pid)}`);

        await closeCopy(second);
        await expect.poll(() => healthy(socket), { timeout: 5_000 }).toBe(false);
    });

    it("keeps RunWisp for a restarting app, and stops it once the app is gone for good", async () => {
        const only = await startCopy();
        only.kill("SIGKILL");
        await exited(only);
        // Still up for a restarting app, which finds it.
        expect(await healthy(socket)).toBe(true);
        const again = await startCopy();
        expect((await trigger(socket, "who")).log).toContain(`pid ${String(again.pid)}`);

        again.kill("SIGKILL");
        await exited(again);
        await expect.poll(() => healthy(socket), { timeout: 20_000, interval: 500 }).toBe(false);
    }, 40_000);

    it("skips a run while the previous one ignores its stop signal", async () => {
        await startCopy();
        const stopped = await trigger(socket, "stubborn");
        expect(stopped.log).toContain("did not stop within graceful_stop");

        const skipped = await trigger(socket, "stubborn");
        expect(skipped.exitCode).toBe(1);
        expect(skipped.log).toContain("skipped: the previous run is still running");
    });

    it("writes an app crash into the run that was in progress", async () => {
        await startCopy();
        const run = await trigger(socket, "crash");
        expect(run.exitCode).not.toBe(0);
        expect(run.log).toContain("the app crashed while this run was in progress:");
        expect(run.log).toContain("Error: kaboom");
    });
});
