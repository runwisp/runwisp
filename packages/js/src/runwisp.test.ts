// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import type { Socket } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { setImmediate as nextTurn } from "node:timers/promises";

import { afterEach, describe, expect, it, vi } from "vitest";

import { daemonPaths, field } from "./daemon.ts";
import { RunWisp, type Logger, type RunWispOptions } from "./runwisp.ts";

function offline() {
    const warnings: string[] = [];
    const log: Logger = {
        info: () => undefined,
        warn: (m) => warnings.push(m),
        error: () => undefined,
    };
    // No binary: unit tests never run RunWisp (e2e/ does).
    return { cron: new RunWisp({ enabled: false, log, binary: "/nonexistent/runwisp" }), warnings };
}

describe("RunWisp (enabled: false)", () => {
    it("registers node-cron tasks under a RunWisp-safe name", () => {
        const { cron } = offline();
        const task = cron.schedule("0 3 * * *", () => undefined, { name: "send emails" });
        expect(task.id).toBe("send-emails");
        expect(task.name).toBe("send emails");
        expect(task.getStatus()).toBe("idle");
        expect(cron.getTask("send-emails")).toBe(task);
        expect(cron.createTask("0 4 * * *", () => undefined, { name: "later" }).getStatus()).toBe(
            "stopped",
        );
    });

    it("works destructured, as node-cron's named exports do", () => {
        const { cron } = offline();
        const { schedule, getTask, validate } = cron;
        expect(schedule("0 3 * * *", () => undefined, { name: "x" }).id).toBe("x");
        expect(getTask("x")).toBeDefined();
        expect(typeof validate).toBe("function");
    });

    it("accepts task names that Object.prototype also has", () => {
        const { cron } = offline();
        expect(cron.task("constructor", { cron: "* * * * *" }, () => undefined).id).toBe(
            "constructor",
        );
    });

    it("derives a stable name for unnamed tasks and warns once", () => {
        const a = offline();
        const b = offline();
        const fn = () => "same code";
        const first = a.cron.schedule("* * * * *", fn);
        a.cron.schedule("0 * * * *", fn);
        expect(first.id).toMatch(/^task-[0-9a-f]{8}$/);
        expect(b.cron.schedule("* * * * *", fn).id).toBe(first.id);
        expect(a.warnings).toHaveLength(1);
    });

    // What a Vite dev server does after an edit: the file runs again.
    it("replaces a task registered again under its name", async () => {
        const { cron } = offline();
        const old = cron.task("sync", { cron: "* * * * *" }, () => "old");
        const replacement = cron.task("sync", { cron: "0 * * * *" }, () => "new");
        expect(old.getStatus()).toBe("destroyed");
        old.stop();
        old.destroy();
        expect(cron.getTask("sync")).toBe(replacement);
        expect(replacement.getStatus()).toBe("idle");
        await replacement.execute();
        expect(replacement.lastRun()?.result).toBe("new");
    });

    // What a dev server does after an edit: the file runs again, in a later turn.
    it("replaces an unnamed task registered again in a later turn, and warns once", async () => {
        const { cron, warnings } = offline();
        const fn = () => "same code";
        const old = cron.schedule("* * * * *", fn);
        await nextTurn();
        const again = cron.schedule("* * * * *", fn);
        expect(again.id).toBe(old.id);
        expect(old.getStatus()).toBe("destroyed");
        expect(cron.getTask(again.id)).toBe(again);
        await nextTurn();
        cron.schedule("* * * * *", fn);
        // Once per id: a dev server re-runs the file on every edit.
        expect(warnings.filter((w) => w.includes("replaced"))).toEqual([
            `runwisp: unnamed task "${old.id}" replaced one registered earlier with the same cron and code. A dev server re-running the file does this; when scheduling in a loop, pass { name } to each task.`,
        ]);
    });

    it("doesn't warn when a named task replaces one", async () => {
        const { cron, warnings } = offline();
        cron.schedule("* * * * *", () => undefined, { name: "a" });
        await nextTurn();
        cron.schedule("* * * * *", () => undefined, { name: "a" });
        expect(warnings).toEqual([]);
    });

    it("throws when unnamed tasks scheduled together would share a name", () => {
        const { cron } = offline();
        const run = (job: string) => job;
        expect(() => {
            for (const job of ["a", "b"]) cron.schedule("* * * * *", () => run(job));
        }).toThrow("pass { name } to each");
        expect(cron.getTasks().size).toBe(1);
    });

    it("throws when two names turn into the same RunWisp name", () => {
        const { cron } = offline();
        cron.schedule("* * * * *", () => undefined, { name: "a b" });
        expect(() => cron.schedule("* * * * *", () => undefined, { name: "a/b" })).toThrow(
            `task names "a b" and "a/b" are both "a-b"`,
        );
        expect(() => cron.task("a-b", {}, () => undefined)).toThrow("are both");
        expect(cron.getTask("a-b")?.name).toBe("a b");
    });

    it("rejects names RunWisp would reject, and ones taken by another kind of unit", () => {
        const { cron } = offline();
        expect(() => cron.schedule("* * * * *", () => undefined, { name: "  " })).toThrow(
            "invalid task name",
        );
        cron.schedule("* * * * *", () => undefined, { name: "dup" });
        expect(() => {
            cron.service("dup", {}, () => undefined);
        }).toThrow("already scheduled");
        cron.service("worker", {}, () => undefined);
        expect(() => cron.task("worker", {}, () => undefined)).toThrow("already scheduled");
        expect(() => cron.task("has space", {}, () => undefined)).toThrow("invalid task name");
        const withShellTask = new RunWisp({
            enabled: false,
            log: false,
            binary: "/nonexistent/runwisp",
            config: { tasks: { vacuum: { run: "true" } } },
        });
        expect(() => withShellTask.task("vacuum", {}, () => undefined)).toThrow(
            "already scheduled",
        );
    });

    it("warns about node-cron options RunWisp makes unnecessary", () => {
        const { cron, warnings } = offline();
        cron.schedule("* * * * *", () => undefined, { name: "a", distributed: true, unref: true });
        expect(warnings).toEqual([expect.stringContaining(`"distributed" is ignored`)]);
    });

    it("sends warnings where setLogger says, and back to the log option for undefined", () => {
        const { cron, warnings } = offline();
        const other: string[] = [];
        cron.setLogger({
            info: () => undefined,
            warn: (m) => other.push(m),
            error: () => undefined,
        });
        cron.schedule("* * * * *", () => undefined, { name: "a", distributed: true });
        cron.setLogger(undefined);
        cron.schedule("* * * * *", () => undefined, { name: "b", maxRandomDelay: 5 });
        cron.setLogger(false);
        cron.schedule("* * * * *", () => undefined, { name: "c", distributedLease: 5 });
        expect(other).toEqual([expect.stringContaining(`"distributed" is ignored`)]);
        expect(warnings).toEqual([expect.stringContaining(`"maxRandomDelay" is ignored`)]);
    });

    it("accepts a run coordinator, warning once that it is never called", () => {
        const { cron, warnings } = offline();
        const coordinator = { shouldRun: () => false };
        cron.setRunCoordinator(coordinator);
        cron.setRunCoordinator(coordinator);
        cron.schedule("* * * * *", () => undefined, { name: "a", runCoordinator: coordinator });
        expect(warnings).toEqual([expect.stringContaining(`"runCoordinator" is ignored`)]);
    });

    it("runs a background task file, named by its path", async () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        try {
            const file = join(dir, "job.mjs");
            writeFileSync(file, "export const task = (ctx) => ctx.execution.reason;");
            writeFileSync(join(dir, "other.mjs"), "export const task = () => 1;");
            const { cron } = offline();
            const task = cron.schedule("* * * * *", file);
            expect(await task.execute()).toBe("invoked");
            expect(cron.schedule("* * * * *", join(dir, "other.mjs")).id).not.toBe(task.id);
        } finally {
            rmSync(dir, { recursive: true, force: true });
        }
    });

    it("works without the runwisp binary", () => {
        const cron = new RunWisp({ enabled: false, log: false, binary: "/nonexistent/runwisp" });
        expect(cron.schedule("* * * * *", () => undefined, { name: "a" }).id).toBe("a");
    });

    it("still runs callbacks in-process with execute()", async () => {
        const { cron } = offline();
        const task = cron.task("manual", {}, () => "done");
        expect(await task.execute()).toBe("done");
    });
});

describe("RunWisp instances", () => {
    const dirs: string[] = [];
    afterEach(() => {
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });
    const dataDir = () => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(dir);
        return dir;
    };

    it("shares one instance per data directory, so every file can create its own", async () => {
        const data = dataDir();
        const a = new RunWisp({ data, enabled: true, log: false });
        expect(new RunWisp({ data, enabled: true, log: false })).toBe(a);
        expect(new RunWisp({ data: dataDir(), enabled: true, log: false })).not.toBe(a);
        expect(() => new RunWisp({ data, port: 1234, enabled: true, log: false })).toThrow(
            "already exists in this process with a different port",
        );

        await a.close();
        expect(new RunWisp({ data, enabled: true, log: false })).not.toBe(a);
    });

    it("keeps instances with enabled: false independent, for unit tests", () => {
        const data = dataDir();
        expect(new RunWisp({ data, enabled: false })).not.toBe(
            new RunWisp({ data, enabled: false }),
        );
    });

    it("closes and exits on SIGTERM with exitOnSignal, once per instance", async () => {
        const data = dataDir();
        const before = process.listenerCount("SIGTERM");
        const a = new RunWisp({ data, enabled: true, log: false });
        expect(process.listenerCount("SIGTERM")).toBe(before);
        new RunWisp({ data, enabled: true, log: false, exitOnSignal: true });
        new RunWisp({ data, enabled: true, log: false, exitOnSignal: true });
        expect(process.listenerCount("SIGTERM")).toBe(before + 1);

        const exit = vi.fn<typeof process.exit>();
        vi.spyOn(process, "exit").mockImplementationOnce(exit);
        process.emit("SIGTERM");
        // Like a process SIGTERM killed: 128 + 15.
        await vi.waitFor(() => {
            expect(exit).toHaveBeenCalledWith(143);
        });
        expect(process.listenerCount("SIGTERM")).toBe(before);
        expect(new RunWisp({ data, enabled: true, log: false })).not.toBe(a);
    });

    it("exits with 130 on SIGINT with exitOnSignal", async () => {
        new RunWisp({ data: dataDir(), enabled: true, log: false, exitOnSignal: true });
        const exit = vi.fn<typeof process.exit>();
        vi.spyOn(process, "exit").mockImplementationOnce(exit);
        process.emit("SIGINT");
        await vi.waitFor(() => {
            expect(exit).toHaveBeenCalledWith(130);
        });
    });

    it("shares the instance with a second copy of the package in the process", async () => {
        const data = dataDir();
        const a = new RunWisp({ data, enabled: true, log: false });
        vi.resetModules();
        const copy = await import("./runwisp.ts");
        expect(copy.RunWisp).not.toBe(RunWisp);
        expect(new copy.RunWisp({ data, enabled: true, log: false })).toBe(a);
        await a.close();
    });

    it("closes when adapter-node (SvelteKit) shuts down", async () => {
        const data = dataDir();
        const a = new RunWisp({ data, enabled: true, log: false });
        process.emit("sveltekit:shutdown");
        await vi.waitFor(() => {
            expect(new RunWisp({ data, enabled: true, log: false })).not.toBe(a);
        });
    });
});

/**
 * A runwisp binary that validates every config, reports its version, and
 * answers `sdk-cron` with one firing at NEXT (an expression starting "bad" is
 * invalid, "@every" has no fields), logging each sdk-cron call to dir/cron.log:
 * so tests can register tasks without the real one.
 */
function fakeBinary(dir: string): string {
    const path = join(dir, "runwisp");
    writeFileSync(
        path,
        `#!/bin/sh
case "$1" in
--version) echo "runwisp version 1.0.0" ;;
validate) cat >/dev/null; echo '{"errors":[]}' ;;
sdk-cron)
    echo "$@" >> "${dir}/cron.log"
    for expr; do :; done
    case "$expr" in
    bad*) echo '{"valid":false,"errors":[{"field":"minute","value":"bad","message":"failed to parse int from bad"}],"next":[]}'; exit 1 ;;
    @every*) echo '{"valid":true,"errors":[],"next":["${NEXT.toISOString()}"]}' ;;
    *) echo '{"valid":true,"fields":{"second":[0],"minute":[0],"hour":[3],"dayOfMonth":[1],"month":[1],"dayOfWeek":[0,1,2,3,4,5,6]},"errors":[],"next":["${NEXT.toISOString()}"]}' ;;
    esac ;;
esac
`,
    );
    chmodSync(path, 0o755);
    return path;
}

const NEXT = new Date("2999-01-01T03:00:00Z");

/** The daemon's side of the socket API, enough for one app to connect and run. */
function fakeDaemon(
    data: string,
    role = '{"type":"role","active":true,"alone":true}',
    /** Replies to the first configs, in order; config_ok after that. */
    configReplies: string[] = [],
) {
    const received: unknown[] = [];
    const apps: Socket[] = [];
    const server: Server = createServer((req, res) => {
        const body: Record<string, unknown> = {
            "/health": "ok",
            // A pid nothing has, should a test ever stop this daemon.
            "/api/daemon/identity": {
                pid: 2 ** 30,
                version: "1.0.0",
                configPath: join(data, "runwisp.toml"),
                configSource: "app",
            },
            "/api/tasks": { items: [] },
        };
        res.end(JSON.stringify(body[req.url ?? ""] ?? {}));
    });
    server.on("upgrade", (_req, socket: Socket) => {
        apps.push(socket);
        // Like the daemon: an app that hangs up is gone.
        socket.on("end", () => socket.end());
        socket.write(
            "HTTP/1.1 101 Switching Protocols\r\nUpgrade: runwisp-app/1\r\nConnection: Upgrade\r\n\r\n",
        );
        socket.write(`${role}\n`);
        createInterface({ input: socket }).on("line", (line) => {
            const message: unknown = JSON.parse(line);
            received.push(message);
            if (line.startsWith('{"type":"config"'))
                socket.write(`${configReplies.shift() ?? '{"type":"config_ok","result":{}}'}\n`);
        });
    });
    const listen = () =>
        new Promise<void>((resolve) => server.listen(daemonPaths(data).socket, resolve));
    return {
        received,
        apps,
        listen,
        send: (message: object) => {
            for (const socket of apps) socket.write(`${JSON.stringify(message)}\n`);
        },
        close: () => {
            for (const socket of apps) socket.destroy();
            server.close();
        },
    };
}

describe("RunWisp cron questions", () => {
    const dirs: string[] = [];
    afterEach(() => {
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });
    const setup = (options: Partial<RunWispOptions> = {}) => {
        const dir = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(dir);
        const cron = new RunWisp({
            enabled: false,
            log: false,
            binary: fakeBinary(dir),
            ...options,
        });
        const calls = () => readFileSync(join(dir, "cron.log"), "utf8").trim().split("\n");
        return { cron, calls };
    };

    it("knows the next run right after schedule(), in the daemon's timezone", () => {
        const { cron, calls } = setup({ config: { daemon: { timezone: "Asia/Tokyo" } } });
        const task = cron.schedule("0 3 * * *", () => undefined, { name: "a" });
        expect(task.getNextRun()).toEqual(NEXT);
        expect(task.msToNext()).toBeGreaterThan(0);
        expect(calls()[0]).toMatch(/--timezone Asia\/Tokyo -- 0 3 \* \* \*$/);
        const own = cron.schedule("0 3 * * *", () => undefined, { name: "b", timezone: "UTC" });
        own.getNextRuns(2);
        expect(calls()[1]).toMatch(/--timezone UTC -- 0 3 \* \* \*$/);
        // node-cron: null while stopped, a date again once started.
        own.stop();
        expect(own.getNextRun()).toBeNull();
        expect(own.match(NEXT)).toBe(true);
    });

    it("validates and parses as node-cron, with RunWisp's grammar", () => {
        const { cron, calls } = setup();
        expect(cron.validate("0 3 1 1 *")).toBe(true);
        expect(cron.validate("bad")).toBe(false);
        expect(cron.validateDetailed("bad")).toEqual({
            valid: false,
            errors: [{ field: "minute", value: "bad", message: "failed to parse int from bad" }],
        });
        expect(cron.parse("0 3 1 1 *")).toEqual({
            second: [0],
            minute: [0],
            hour: [3],
            dayOfMonth: [1],
            month: [1],
            dayOfWeek: [0, 1, 2, 3, 4, 5, 6],
        });
        expect(() => cron.parse("bad")).toThrow("failed to parse int from bad");
        expect(cron.validateDetailed("@every 90s")).toEqual({ valid: true, errors: [] });
        expect(() => cron.parse("@every 90s")).toThrow("is an interval");
        // One ask per expression, however often it is asked about.
        expect(calls()).toHaveLength(3);
    });
});

describe("RunWisp with a daemon", () => {
    const dirs: string[] = [];
    const cleanups: (() => unknown)[] = [];
    afterEach(async () => {
        vi.useRealTimers();
        for (const cleanup of cleanups.splice(0)) await cleanup();
        for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true });
    });
    const setup = (role?: string, configReplies?: string[]) => {
        const data = mkdtempSync(join(tmpdir(), "rwn-"));
        dirs.push(data);
        const daemon = fakeDaemon(data, role, configReplies);
        cleanups.push(daemon.close);
        const lines: string[] = [];
        const log: Logger = {
            info: (m) => lines.push(m),
            warn: (m) => lines.push(m),
            error: (m) => lines.push(m),
        };
        const cron = new RunWisp({ data, binary: fakeBinary(data), log, port: 1 });
        cleanups.push(() => cron.close());
        return { data, daemon, cron, lines };
    };

    // RunWisp stops the services of a copy that leaves, so their runs end as
    // stopped; close() waits for them to return.
    it("stops its services on close() when RunWisp asks", async () => {
        const { daemon, cron } = setup();
        await daemon.listen();
        let started = false;
        cron.service("worker", {}, async ({ signal }) => {
            started = true;
            await new Promise((resolve) => {
                signal.addEventListener("abort", resolve);
            });
        });
        await cron.start();
        daemon.send({ type: "run", run: "r1", task: "worker" });
        await vi.waitFor(() => {
            expect(started).toBe(true);
        });

        const closing = cron.close();
        await vi.waitFor(() => {
            expect(daemon.received).toContainEqual({ type: "close" });
        });
        expect(daemon.received).not.toContainEqual(expect.objectContaining({ type: "exit" }));
        daemon.send({ type: "stop", run: "r1" });
        await closing;
        expect(daemon.received).toContainEqual({ type: "exit", run: "r1", code: 143 });
    });

    it("restarts RunWisp for a change only a restart applies, leaving the old connection first", async () => {
        const { daemon, cron, lines } = setup(undefined, [
            '{"type":"config_error","error":"listener changed","restart":true}',
        ]);
        await daemon.listen();
        cron.schedule("0 3 * * *", () => undefined, { name: "a" });
        await vi.waitFor(() => {
            expect(daemon.received.filter((m) => field(m, "type") === "config")).toHaveLength(2);
        });
        expect(lines).toContain(
            "runwisp: restarting RunWisp to apply the new config (listener changed)",
        );
        expect(daemon.apps).toHaveLength(2);
        // The first connection is gone, so the new one isn't mistaken for a standby copy.
        expect(daemon.apps[0]?.destroyed).toBe(true);
    });

    it("keeps retrying to connect, backing off, until RunWisp answers", async () => {
        vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
        const { daemon, cron, lines } = setup();
        // Nothing listens yet, and the fake binary starts no daemon.
        await expect(cron.start()).rejects.toThrow("RunWisp did not start");
        await vi.waitFor(() => {
            expect(lines.at(-1)).toMatch(/did not start.*\(retrying in 1s\)$/s);
        });
        await vi.advanceTimersByTimeAsync(1000);
        await vi.waitFor(() => {
            expect(lines.at(-1)).toMatch(
                /^runwisp: RunWisp did not start \(log: .*\) \(retrying in 2s\)$/,
            );
        });

        await daemon.listen();
        await vi.advanceTimersByTimeAsync(2000);
        await vi.waitFor(() => {
            expect(daemon.received).toContainEqual(expect.objectContaining({ type: "config" }));
        });
    });

    it("says when it stands by for another copy, and when it takes over", async () => {
        const { daemon, cron, lines } = setup('{"type":"role","active":false,"pid":4242}');
        await daemon.listen();
        await cron.start();
        expect(lines).toContainEqual(
            expect.stringContaining("standby: another copy of this app is active (pid 4242)"),
        );
        daemon.send({ type: "role", active: true });
        await vi.waitFor(() => {
            expect(lines).toContain(
                "runwisp: this copy of the app is now active and runs the tasks",
            );
        });
    });

    it("emits execution:overlap when RunWisp skipped a firing", async () => {
        const { daemon, cron } = setup();
        await daemon.listen();
        const task = cron.schedule("* * * * *", () => undefined, {
            name: "digest",
            noOverlap: true,
        });
        const dates: Date[] = [];
        task.on("execution:overlap", (ctx) => dates.push(ctx.date));
        await cron.start();
        daemon.send({ type: "overlap", task: "digest", at: "2026-01-02T03:04:00Z" });
        await vi.waitFor(() => {
            expect(dates).toEqual([new Date("2026-01-02T03:04:00Z")]);
        });
    });

    it("emits execution:missed for a firing RunWisp missed while it was down", async () => {
        const { daemon, cron, lines } = setup();
        await daemon.listen();
        const quiet = cron.schedule("0 3 * * *", () => undefined, { name: "quiet" });
        const dates: Date[] = [];
        quiet.on("execution:missed", (ctx) => dates.push(ctx.date));
        cron.schedule("0 3 * * *", () => undefined, { name: "loud", timezone: "UTC" });
        await cron.start();
        daemon.send({ type: "missed", task: "quiet", at: "2026-01-02T03:00:00Z" });
        daemon.send({ type: "missed", task: "loud", at: "2026-01-02T03:00:00Z" });
        await vi.waitFor(() => {
            expect(dates).toEqual([new Date("2026-01-02T03:00:00Z")]);
            expect(lines).toContain(
                'runwisp: task "loud" missed its run at 2026-01-02T03:00:00.000Z: RunWisp was not running',
            );
        });
        expect(lines.filter((l) => l.includes("missed"))).toHaveLength(1);
    });

    it("dates a run by the firing RunWisp started it for", async () => {
        const { daemon, cron } = setup();
        await daemon.listen();
        const seen: [string, Date][] = [];
        cron.schedule("0 3 * * *", (ctx) => seen.push([ctx.execution.reason, ctx.date]), {
            name: "nightly",
        });
        await cron.start();
        daemon.send({ type: "run", run: "r1", task: "nightly", at: "2026-01-02T03:00:00Z" });
        await vi.waitFor(() => {
            expect(seen).toHaveLength(1);
        });
        // A run started from the UI has no firing.
        const before = Date.now();
        daemon.send({ type: "run", run: "r2", task: "nightly" });
        await vi.waitFor(() => {
            expect(seen).toHaveLength(2);
        });
        expect(seen[0]).toEqual(["scheduled", new Date("2026-01-02T03:00:00Z")]);
        expect(seen[1]?.[0]).toBe("invoked");
        expect(seen[1]?.[1].getTime()).toBeGreaterThanOrEqual(before);
    });

    it("shuts down within the timeout, destroying every task", async () => {
        const { daemon, cron } = setup();
        await daemon.listen();
        let release: () => void = () => undefined;
        const task = cron.schedule(
            "0 3 * * *",
            () =>
                new Promise((resolve) => {
                    release = () => {
                        resolve(undefined);
                    };
                }),
            { name: "stuck" },
        );
        const events: string[] = [];
        task.on("task:destroyed", () => events.push("destroyed"));
        await cron.start();
        daemon.send({ type: "run", run: "r1", task: "stuck", at: "2026-01-02T03:00:00Z" });
        await vi.waitFor(() => {
            expect(task.isBusy()).toBe(true);
        });
        const begun = Date.now();
        await cron.shutdown(200);
        expect(Date.now() - begun).toBeLessThan(2000);
        expect(cron.getTasks().size).toBe(0);
        expect(events).toEqual(["destroyed"]);
        // close() still lets it finish.
        release();
    });
});
