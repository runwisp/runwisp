// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from "vitest";

interface Settings {
    dev: boolean;
    exitOnSignal: boolean;
    clustered: boolean;
    output: string | undefined;
    rootDir: string;
    scheduled: [cron: string, task: string][];
    config: { notifiers: Record<string, unknown> } | undefined;
}

const nuxt = vi.hoisted(() => {
    const created: unknown[] = [];
    const tasks: string[] = [];
    const taskOptions: unknown[] = [];
    const options = { data: "/srv/app/.runwisp", port: 9500 };
    const settings: Settings = {
        dev: false,
        exitOnSignal: false,
        clustered: false,
        output: undefined,
        rootDir: "/srv/app",
        scheduled: [["0 * * * *", "db:cleanup"]],
        config: undefined,
    };
    const worker = { isConnected: () => true, send: vi.fn() };
    const cluster = { isPrimary: true, workers: { 1: worker } };
    return { created, tasks, taskOptions, options, settings, cluster, worker };
});

vi.mock("node:cluster", () => ({ default: nuxt.cluster }));

vi.mock("#runwisp/nuxt", () => ({ options: nuxt.options }));
vi.mock("#runwisp/nuxt-internal", () => nuxt.settings);
vi.mock("#imports", () => ({ runTask: vi.fn() }));
vi.mock("@runwisp/js", () => ({
    RunWisp: class {
        static closeAll = vi.fn(() => Promise.resolve());
        constructor(options: unknown) {
            nuxt.created.push(options);
        }
        task(name: string, options: unknown) {
            nuxt.tasks.push(name);
            nuxt.taskOptions.push(options);
        }
    },
}));

const { default: closePlugin, closeRunWisp } = await import("./nuxt-close.ts");
const { default: tasksPlugin, scheduleNitroTasks } = await import("./nuxt-tasks.ts");

const nitroApp = { hooks: { hook: vi.fn() } };

describe("the Nuxt module's Nitro plugins", () => {
    beforeEach(() => {
        nuxt.created.length = 0;
        nuxt.tasks.length = 0;
        nuxt.taskOptions.length = 0;
        nuxt.settings.config = undefined;
        nuxt.settings.exitOnSignal = false;
        nuxt.settings.clustered = false;
        nuxt.settings.output = undefined;
        nuxt.cluster.isPrimary = true;
        nuxt.worker.send.mockClear();
        nitroApp.hooks.hook.mockClear();
    });

    // Without them, the scheduled tasks ran in a RunWisp with the default
    // data directory, not the one the app's own plugins use.
    it("schedule Nitro's tasks with the module's options", () => {
        scheduleNitroTasks(false);
        expect(nuxt.created).toEqual([{ data: "/srv/app/.runwisp", port: 9500 }]);
        expect(nuxt.tasks).toEqual(["db:cleanup"]);
    });

    // Nitro's scheduler never runs a missed time late, and its runTask()
    // joins a run of the task already going instead of starting another.
    it("schedule Nitro's tasks with Nitro's catch-up and overlap", () => {
        scheduleNitroTasks(false);
        expect(nuxt.taskOptions).toEqual([
            { cron: "0 * * * *", catch_up: 0, on_overlap: "skip", max_concurrent: 1 },
        ]);
    });

    it("hand the module's config to the first RunWisp, before the app's plugins", () => {
        closeRunWisp(nitroApp, false);
        expect(nuxt.created).toEqual([]);

        nuxt.settings.config = { notifiers: { ops: { type: "slack" } } };
        closeRunWisp(nitroApp, false);
        expect(nuxt.created).toEqual([
            {
                data: "/srv/app/.runwisp",
                port: 9500,
                config: { notifiers: { ops: { type: "slack" } } },
            },
        ]);
    });

    // `nuxi build` runs the plugins to prerender routes: anything they start
    // there runs RunWisp in the build, or keeps the build from exiting.
    it("start nothing while Nitro prerenders", () => {
        nuxt.settings.exitOnSignal = true;
        nuxt.settings.config = { notifiers: {} };
        const listeners = () => [...process.listeners("SIGTERM"), ...process.listeners("SIGINT")];
        const before = listeners();

        closeRunWisp(nitroApp, true);
        scheduleNitroTasks(true);
        expect(nuxt.created).toEqual([]);
        expect(nuxt.tasks).toEqual([]);
        expect(nitroApp.hooks.hook).not.toHaveBeenCalled();
        expect(listeners()).toEqual(before);

        // The same settings, outside prerendering, do all of it.
        closeRunWisp(nitroApp, false);
        scheduleNitroTasks(false);
        const added = listeners().filter((listener) => !before.includes(listener));
        for (const listener of added) {
            process.off("SIGTERM", listener);
            process.off("SIGINT", listener);
        }
        expect(nuxt.created).toHaveLength(2);
        expect(nuxt.tasks).toEqual(["db:cleanup"]);
        expect(nitroApp.hooks.hook).toHaveBeenCalledWith("close", expect.any(Function));
        expect(added).toHaveLength(2);
    });

    // Vitest can't set import.meta.prerender per module, so this checks the
    // plugins pass it on, as compiled.
    it("pass Nitro's import.meta.prerender on", () => {
        expect(String(closePlugin)).toMatch(/closeRunWisp\(nitroApp, \w*meta\w*\.prerender\)/);
        expect(String(tasksPlugin)).toMatch(/scheduleNitroTasks\(\w*meta\w*\.prerender\)/);
    });

    // `nuxi preview` runs the server from inside .output, which the next
    // build deletes along with the data.
    it("keeps the data of a server run from the build output in the project", () => {
        const data = nuxt.options.data;
        vi.stubEnv("RUNWISP_DATA", "");
        nuxt.settings.output = process.cwd();
        // Not when the module sets the data directory.
        closeRunWisp(nitroApp, false);
        expect(process.env["RUNWISP_DATA"]).toBe("");

        Reflect.deleteProperty(nuxt.options, "data");
        try {
            closeRunWisp(nitroApp, false);
            expect(process.env["RUNWISP_DATA"]).toBe("/srv/app/.runwisp");

            // Nor when the data directory is chosen, or the server runs from elsewhere.
            vi.stubEnv("RUNWISP_DATA", "/data");
            closeRunWisp(nitroApp, false);
            expect(process.env["RUNWISP_DATA"]).toBe("/data");
            vi.stubEnv("RUNWISP_DATA", "");
            nuxt.settings.output = "/srv/app/.output";
            closeRunWisp(nitroApp, false);
            expect(process.env["RUNWISP_DATA"]).toBe("");
        } finally {
            Object.assign(nuxt.options, { data });
            vi.unstubAllEnvs();
        }
    });

    describe("with the node-cluster entry", () => {
        // Nitro's primary never runs the close hook, so it closes its own
        // RunWisp, and tells the workers, which wait for it, to stop.
        it("has the primary close RunWisp and tell the workers", async () => {
            nuxt.settings.clustered = true;
            const before = new Set(process.listeners("SIGTERM"));
            closeRunWisp(nitroApp, false);
            expect(nitroApp.hooks.hook).not.toHaveBeenCalled();
            const [listener] = process.listeners("SIGTERM").filter((l) => !before.has(l));
            if (!listener) throw new Error("no SIGTERM listener");
            process.off("SIGTERM", listener);

            const { RunWisp } = await import("@runwisp/js");
            let finish = () => {};
            // eslint-disable-next-line @typescript-eslint/unbound-method -- a vi.fn
            vi.mocked(RunWisp.closeAll).mockReturnValueOnce(
                new Promise((resolve) => {
                    finish = resolve;
                }),
            );
            listener("SIGTERM");
            expect(nuxt.worker.send).toHaveBeenCalledTimes(1);
            expect(nuxt.worker.send).toHaveBeenLastCalledWith(
                { runwisp: "stop", signal: "SIGTERM" },
                expect.any(Function),
            );
            finish();
            await vi.waitFor(() => {
                expect(nuxt.worker.send).toHaveBeenLastCalledWith(
                    { runwisp: "closed" },
                    expect.any(Function),
                );
            });
        });

        it("has a worker stop on the primary's word, and exit once the primary is closed", async () => {
            nuxt.settings.clustered = true;
            nuxt.cluster.isPrimary = false;
            const before = new Set(process.listeners("message"));
            closeRunWisp(nitroApp, false);
            const [listener] = process.listeners("message").filter((l) => !before.has(l));
            const close: unknown = nitroApp.hooks.hook.mock.calls[0]?.[1];
            if (!listener || typeof close !== "function") throw new Error("not set up");
            process.off("message", listener);

            const emit = vi.spyOn(process, "emit").mockReturnValue(true);
            listener({ runwisp: "stop", signal: "SIGTERM" }, undefined);
            expect(emit).toHaveBeenCalledWith("SIGTERM");
            emit.mockRestore();

            let done = false;
            const closing = Promise.resolve(Reflect.apply(close, undefined, [])).then(() => {
                done = true;
            });
            await new Promise((resolve) => setTimeout(resolve, 10));
            expect(done).toBe(false);
            listener({ runwisp: "closed" }, undefined);
            await closing;
            expect(done).toBe(true);
        });
    });
});
