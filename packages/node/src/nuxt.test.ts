// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";

import runwispNuxt, {
    VIRTUAL_MODULE,
    type NitroConfig,
    type NuxtHooks,
    type RunWispNuxtOptions,
} from "./nuxt.ts";

const NODE_SERVER = "/app/node_modules/nitropack/dist/presets/node/runtime/node-server";

interface Build {
    dev?: boolean;
    preset?: string;
    entry?: string | undefined;
    options?: RunWispNuxtOptions;
}

/** Runs the module's hooks as Nuxt would, and the virtual module as Nitro reads it. */
function nitroConfig(config: NitroConfig, build: Build = {}) {
    const { dev = false, preset = "node-server", options = {} } = build;
    const entry = "entry" in build ? build.entry : NODE_SERVER;
    const hooks: Partial<NuxtHooks> = {};
    runwispNuxt(options, {
        options: { dev },
        hook: (name, fn) => {
            hooks[name] = fn;
        },
    });
    hooks["nitro:config"]?.(config);
    hooks["nitro:init"]?.({ options: { preset, entry } });
    const declarations: string[] = [];
    hooks["nitro:prepare:types"]?.({ declarations });
    const virtual = config.virtual?.[VIRTUAL_MODULE];
    return { config, declarations, settings: typeof virtual === "function" ? virtual() : virtual };
}

describe("@runwisp/node/nuxt", () => {
    it("moves scheduledTasks from Nitro to RunWisp", () => {
        const { config, settings } = nitroConfig({
            scheduledTasks: { "0 * * * *": ["db:cleanup", "cache:warm"], "@daily": "db:cleanup" },
            plugins: ["./mine.ts"],
            externals: { inline: ["@runwisp/node", "vue"], traceInclude: ["sharp"] },
        });
        expect(config.scheduledTasks).toBeUndefined();
        expect(config.plugins).toEqual([
            "./mine.ts",
            expect.stringMatching(/nuxt-close\.ts$/),
            expect.stringMatching(/nuxt-tasks\.ts$/),
        ]);
        expect(settings).toBe(
            [
                "export const dev = false;",
                "export const exitOnSignal = false;",
                'export const scheduled = [["0 * * * *","db:cleanup"],["0 * * * *","cache:warm"],["@daily","db:cleanup"]];',
                "export const options = {};",
                "export const config = undefined;",
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
        const { settings, declarations } = nitroConfig(
            {},
            {
                options: {
                    data: "/srv/app/.runwisp",
                    port: 9500,
                    config: { notifiers: { ops: { type: "slack", webhook_url: "${SLACK_URL}" } } },
                },
            },
        );
        expect(settings).toContain(
            'export const options = {"data":"/srv/app/.runwisp","port":9500};',
        );
        expect(settings).toContain(
            'export const config = {"notifiers":{"ops":{"type":"slack","webhook_url":"${SLACK_URL}"}}};',
        );
        expect(declarations).toEqual([
            'declare module "#runwisp/nuxt" { export const options: import("@runwisp/node").RunWispOptions; }',
        ]);
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
