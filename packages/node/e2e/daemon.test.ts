// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { call, daemonPaths, field, healthy, identity } from "../src/daemon.ts";
import { RunWisp, type Logger } from "../src/index.ts";
import { binary, freePort, trigger as triggerOn } from "./helpers.ts";

describe("RunWisp with the real daemon", () => {
    const data = mkdtempSync(join(tmpdir(), "rwn-e2e-"));
    const socket = daemonPaths(data).socket;
    const lines: string[] = [];
    const log: Logger = {
        info: (m) => {
            lines.push(m);
            // A slow logger widens the startup window: runs that fire as the
            // daemon starts must still find the app.
            Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 500);
        },
        warn: (m) => lines.push(m),
        error: (m) => lines.push(m),
    };
    let cron: RunWisp;

    const trigger = (task: string) => triggerOn(socket, task);

    beforeAll(async () => {
        cron = new RunWisp({
            data,
            port: await freePort(),
            log,
            binary,
            config: {
                defaults: { env: { REGION: "eu" } },
                notifiers: { hook: { type: "webhook", url: "http://127.0.0.1:9/hook" } },
                tasks: { shell: { run: "echo from a shell task" } },
            },
        });
        cron.schedule(
            "* * * * * *",
            () => {
                console.log("tick");
            },
            { name: "tick" },
        );
        cron.task("hello", {}, () => {
            console.log("hello from the app");
            console.error("and a warning");
        });
        cron.task("on-start", { run_on_start: true }, () => {
            console.log("ran at startup");
        });
        cron.task(
            "context",
            {
                env: { TOKEN: "t0ken" },
                params: [{ env: "DRY_RUN", default: "1" }],
                notify: ["hook"],
            },
            (ctx) => {
                console.log(
                    `region=${String(ctx.env["REGION"])} token=${String(ctx.env["TOKEN"])} dry=${String(ctx.params["DRY_RUN"])}`,
                );
            },
        );
        cron.service("worker", {}, async (ctx) => {
            console.log("worker up");
            await new Promise((resolve) => {
                ctx.signal.addEventListener("abort", resolve);
            });
        });
        cron.task("broken", {}, () => {
            throw new Error("boom");
        });
        await cron.start();
    });

    afterAll(async () => {
        await cron.close();
        rmSync(data, { recursive: true, force: true });
    });

    it("prints the dashboard URL and how to get the password, never the password", () => {
        const notice = lines.find((line) => line.startsWith("RunWisp dashboard:"));
        expect(notice).toBe(
            `RunWisp dashboard: ${cron.url}, password: npx runwisp password --data ${data}`,
        );
        const password = execFileSync(binary, ["password", "--data", data], {
            encoding: "utf8",
        }).trim();
        expect(password).not.toBe("");
        expect(lines.join("\n")).not.toContain(password);
    });

    it("records a run with the callback's output", async () => {
        const run = await trigger("hello");
        expect(run.exitCode).toBe(0);
        expect(run.log).toContain("hello from the app");
        expect(run.log).toContain("and a warning");
    });

    it("records a thrown error as a failed run, without the package's own stack frames", async () => {
        const run = await trigger("broken");
        expect(run.exitCode).toBe(1);
        expect(run.log).toContain("Error: boom");
        expect(run.log).not.toContain("scheduled-task");
    });

    it("runs the daemon with the app's config, not a file", async () => {
        expect((await identity(socket)).configSource).toBe("app");
        const run = await trigger("shell");
        expect(run.exitCode).toBe(0);
        expect(run.log).toContain("from a shell task");
    });

    it("gives the callback its env, secrets and params, redacted in the log", async () => {
        const run = await trigger("context");
        expect(run.exitCode).toBe(0);
        expect(run.log).toContain("region=eu");
        expect(run.log).toContain("dry=1");
    });

    it("keeps an in-app service running", async () => {
        const logOf = async (): Promise<unknown> => {
            const items = field(
                (await call(socket, "GET", "/api/runs?taskName=worker")).body,
                "items",
            );
            if (!Array.isArray(items) || items.length === 0) return "";
            const id = String(field(items[0], "id"));
            return (await call(socket, "GET", `/api/runs/${id}/log/raw`)).body;
        };
        await expect.poll(logOf, { timeout: 5_000 }).toContain("worker up");
    });

    it("rejects what RunWisp would reject, when the task is added", () => {
        expect(cron.validate("*/5 * * * *")).toBe(true);
        expect(cron.validate("61 * * * *")).toBe(false);
        expect(() => cron.schedule("not cron", () => undefined, { name: "x" })).toThrow(
            `invalid cron for task "x"`,
        );
        expect(() => cron.task("frac", { max_concurrent: 1.5 }, () => undefined)).toThrow(
            "tasks.frac.max_concurrent",
        );
        expect(() => cron.task("tz", { timezone: "Mars/Base" }, () => undefined)).toThrow(
            "Mars/Base",
        );
        expect(() => cron.task("both", { run: "true" }, () => undefined)).toThrow(
            "sets both sdk and run",
        );
        expect(() => cron.task("pager", { notify: ["nobody"] }, () => undefined)).toThrow("nobody");
    });

    it("serves runs that fire as soon as the daemon starts", async () => {
        const firstExitCode = async (): Promise<unknown> => {
            const items = field(
                (await call(socket, "GET", "/api/runs?taskName=on-start")).body,
                "items",
            );
            return Array.isArray(items) ? field(items[0], "exitCode") : undefined;
        };
        await expect.poll(firstExitCode, { timeout: 5_000 }).toBe(0);
    });

    it("runs scheduled tasks and unschedules them on stop()", async () => {
        await expect
            .poll(
                async () =>
                    field(
                        (await call(socket, "GET", "/api/runs?taskName=tick&limit=1")).body,
                        "items",
                    ),
                {
                    timeout: 5_000,
                },
            )
            .toHaveLength(1);
        cron.getTask("tick")?.stop();
        await cron.start();
        const task = await call(socket, "GET", "/api/tasks/tick");
        expect(field(task.body, "nextRunAt")).toBeUndefined();
        expect(cron.getTask("tick")?.getNextRun()).toBeNull();
    });

    it("stops the daemon it started on close()", async () => {
        await cron.close();
        await expect.poll(() => healthy(socket), { timeout: 5_000 }).toBe(false);
    });
});
