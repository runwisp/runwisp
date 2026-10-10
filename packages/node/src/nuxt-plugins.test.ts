// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { beforeEach, describe, expect, it, vi } from "vitest";

interface Settings {
    dev: boolean;
    exitOnSignal: boolean;
    scheduled: [cron: string, task: string][];
    options: { data: string; port: number };
    config: { notifiers: Record<string, unknown> } | undefined;
}

const nuxt = vi.hoisted(() => {
    const created: unknown[] = [];
    const tasks: string[] = [];
    const settings: Settings = {
        dev: false,
        exitOnSignal: false,
        scheduled: [["0 * * * *", "db:cleanup"]],
        options: { data: "/srv/app/.runwisp", port: 9500 },
        config: undefined,
    };
    return { created, tasks, settings };
});

vi.mock("#runwisp/nuxt", () => nuxt.settings);
vi.mock("#imports", () => ({ runTask: vi.fn() }));
vi.mock("@runwisp/node", () => ({
    RunWisp: class {
        static closeAll = vi.fn(() => Promise.resolve());
        constructor(options: unknown) {
            nuxt.created.push(options);
        }
        task(name: string) {
            nuxt.tasks.push(name);
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
        nuxt.settings.config = undefined;
        nuxt.settings.exitOnSignal = false;
        nitroApp.hooks.hook.mockClear();
    });

    // Without them, the scheduled tasks ran in a RunWisp with the default
    // data directory, not the one the app's own plugins use.
    it("schedule Nitro's tasks with the module's options", () => {
        scheduleNitroTasks(false);
        expect(nuxt.created).toEqual([{ data: "/srv/app/.runwisp", port: 9500 }]);
        expect(nuxt.tasks).toEqual(["db:cleanup"]);
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
});
