// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterEach, describe, expect, it } from "vitest";

import { RunWisp, type Logger } from "./runwisp.ts";

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

    it("derives a stable name for unnamed tasks and warns once", () => {
        const a = offline();
        const b = offline();
        const fn = () => "same code";
        const first = a.cron.schedule("* * * * *", fn);
        const second = a.cron.schedule("* * * * *", fn);
        expect(first.id).toMatch(/^task-[0-9a-f]{8}$/);
        expect(second.id).toBe(`${first.id}-2`);
        expect(b.cron.schedule("* * * * *", fn).id).toBe(first.id);
        expect(a.warnings).toHaveLength(1);
    });

    it("rejects names RunWisp would reject, and duplicates", () => {
        const { cron } = offline();
        expect(() => cron.schedule("* * * * *", () => undefined, { name: "  " })).toThrow(
            "invalid task name",
        );
        cron.schedule("* * * * *", () => undefined, { name: "dup" });
        expect(() => cron.schedule("* * * * *", () => undefined, { name: "dup" })).toThrow(
            "already scheduled",
        );
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
});
