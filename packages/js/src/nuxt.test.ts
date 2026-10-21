// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import {
    INTERNAL_MODULE,
    TYPES,
    VIRTUAL_MODULE,
    type NitroConfig,
    type NuxtHooks,
} from "./nuxt-meta.ts";
import runwispNuxt, { type RunWispNuxtOptions } from "./nuxt.ts";

const NODE_SERVER = "/app/node_modules/nitropack/dist/presets/node/runtime/node-server";

interface Build {
    dev?: boolean;
    preset?: string;
    entry?: string | undefined;
    output?: string;
    /** The `runwisp` key of the Nuxt config. */
    options?: RunWispNuxtOptions | false;
    inline?: RunWispNuxtOptions;
}

/** Runs the module's hooks as Nuxt would, and the virtual modules as Nitro reads them. */
function nitroConfig(config: NitroConfig, build: Build = {}) {
    const { dev = false, preset = "node-server", options, inline = {}, output } = build;
    const entry = "entry" in build ? build.entry : NODE_SERVER;
    const hooks: Partial<NuxtHooks> = {};
    const templates: { filename: string; getContents: () => string }[] = [];
    runwispNuxt(inline, {
        options: {
            dev,
            buildDir: "/app/.nuxt",
            rootDir: "/app",
            build: { templates },
            runwisp: options,
        },
        hook: (name, fn) => {
            hooks[name] = fn;
        },
    });
    hooks["nitro:config"]?.(config);
    hooks["nitro:init"]?.({
        options: { preset, entry, ...(output === undefined ? {} : { output: { dir: output } }) },
    });
    const references: { path: string }[] = [];
    hooks["nitro:prepare:types"]?.({ references });
    const read = (id: string) => {
        const virtual = config.virtual?.[id];
        return typeof virtual === "function" ? virtual() : virtual;
    };
    return {
        config,
        templates,
        references,
        options: read(VIRTUAL_MODULE),
        settings: read(INTERNAL_MODULE),
    };
}

describe("@runwisp/js/nuxt", () => {
    it("moves scheduledTasks from Nitro to RunWisp", () => {
        const { config, options, settings } = nitroConfig({
            scheduledTasks: { "0 * * * *": ["db:cleanup", "cache:warm"], "@daily": "db:cleanup" },
            plugins: ["./mine.ts"],
            externals: { inline: ["@runwisp/js", "vue"], traceInclude: ["sharp"] },
        });
        expect(config.scheduledTasks).toBeUndefined();
        expect(config.plugins).toEqual([
            "./mine.ts",
            expect.stringMatching(/nuxt-close\.ts$/),
            expect.stringMatching(/nuxt-tasks\.ts$/),
        ]);
        // The app's module exports only what TYPES declares.
        expect(options).toBe("export const options = {};");
        expect(settings).toBe(
            [
                "export const dev = false;",
                "export const exitOnSignal = false;",
                "export const clustered = false;",
                'export const scheduled = [["0 * * * *","db:cleanup"],["0 * * * *","cache:warm"],["@daily","db:cleanup"]];',
                "export const config = undefined;",
                "export const output = undefined;",
                'export const rootDir = "/app";',
            ].join("\n"),
        );
        expect(config.externals?.inline).toEqual([
            "vue",
            expect.stringMatching(/nuxt-close\.ts$/),
            expect.stringMatching(/nuxt-tasks\.ts$/),
        ]);
        expect(config.externals?.traceInclude).toEqual([
            "sharp",
            expect.stringMatching(/^\/.*\/runwisp\/bin\/runwisp$/),
            expect.stringMatching(
                new RegExp(`^/.*/@runwisp/${process.platform}-${process.arch}/runwisp$`),
            ),
        ]);
    });

    it("adds only the close plugin without scheduledTasks", () => {
        const { config, settings } = nitroConfig({}, { dev: true, preset: "nitro-dev", entry: "" });
        expect(config.plugins).toEqual([expect.stringMatching(/nuxt-close\.ts$/)]);
        expect(settings).toContain("export const dev = true;");
    });

    // The app's plugins pass the same options, so that every `new RunWisp()`
    // gets the one RunWisp; config goes to the first one only.
    it("hands its options to the plugins and the app", () => {
        const { options, settings, templates, references } = nitroConfig(
            {},
            {
                options: {
                    data: "/srv/app/.runwisp",
                    port: 9500,
                    config: { notifiers: { ops: { type: "slack", webhook_url: "${SLACK_URL}" } } },
                },
            },
        );
        expect(options).toBe('export const options = {"data":"/srv/app/.runwisp","port":9500};');
        expect(settings).toContain(
            'export const config = {"notifiers":{"ops":{"type":"slack","webhook_url":"${SLACK_URL}"}}};',
        );
        // A file of its own, so the declaration stays ambient.
        expect(templates.map((t) => [t.filename, t.getContents()])).toEqual([
            ["types/runwisp-nuxt.d.ts", TYPES],
        ]);
        expect(TYPES).not.toMatch(/^(import|export) /m);
        expect(references).toEqual([{ path: "/app/.nuxt/types/runwisp-nuxt.d.ts" }]);
    });

    it("takes options from the module entry over the runwisp key", () => {
        const { options } = nitroConfig(
            {},
            { options: { data: "/srv/app/.runwisp", port: 9500 }, inline: { port: 9600 } },
        );
        expect(options).toBe('export const options = {"data":"/srv/app/.runwisp","port":9600};');
    });

    it("does nothing with runwisp: false", () => {
        const { config } = nitroConfig(
            { scheduledTasks: { "@daily": "db:cleanup" } },
            { options: false },
        );
        expect(config).toEqual({ scheduledTasks: { "@daily": "db:cleanup" } });
    });

    it("tells Nuxt its config key", async () => {
        expect(await runwispNuxt.getMeta()).toEqual({
            name: "@runwisp/js/nuxt",
            configKey: "runwisp",
        });
    });

    // Nitro's bun server never runs the close hook, so the plugin handles SIGTERM.
    it("has the close plugin exit on signals with the bun preset", () => {
        expect(
            nitroConfig(
                {},
                {
                    preset: "bun",
                    entry: "/app/node_modules/nitropack/dist/presets/bun/runtime/bun",
                },
            ).settings,
        ).toContain("export const exitOnSignal = true;");
    });

    it("has the close plugin coordinate the workers with the node-cluster entry", () => {
        const entry = "/app/node_modules/nitropack/dist/presets/node/runtime/node-cluster.mjs";
        expect(nitroConfig({}, { preset: "node-cluster", entry }).settings).toContain(
            "export const clustered = true;",
        );
        expect(nitroConfig({}).settings).toContain("export const clustered = false;");
    });

    it("tells the close plugin where Nitro writes the build", () => {
        const { settings } = nitroConfig({}, { output: "/app/.output" });
        expect(settings).toContain('export const output = "/app/.output";');
    });

    it("builds for presets that run Nitro's Node or Bun server", () => {
        expect(() => nitroConfig({}, { preset: "heroku" })).not.toThrow();
        expect(() =>
            nitroConfig(
                {},
                {
                    preset: "node-cluster",
                    entry: "/app/node_modules/nitropack/dist/presets/node/runtime/node-cluster.mjs",
                },
            ),
        ).not.toThrow();
    });

    // Tasks in a serverless, edge or static build would never run.
    it("fails the build with a preset that keeps no server running", () => {
        expect(() =>
            nitroConfig(
                {},
                {
                    preset: "vercel",
                    entry: "/app/node_modules/nitropack/dist/presets/vercel/runtime/vercel",
                },
            ),
        ).toThrow('the "vercel" preset doesn\'t start one');
        expect(() => nitroConfig({}, { preset: "static", entry: undefined })).toThrow(
            'the "static" preset',
        );
    });
});
