// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A Nuxt module: `modules: ["@runwisp/js/nuxt"]`, configured under the
// `runwisp` key of the Nuxt config. Ships the runwisp binary
// with the build, closes RunWisp when the server stops, and runs Nitro's
// `scheduledTasks` under RunWisp instead of Nitro's own scheduler.

import { dirname, extname, join, parse } from "node:path";
import { fileURLToPath } from "node:url";

import { binaryFiles } from "./daemon.ts";
import { INTERNAL_MODULE, TYPES, VIRTUAL_MODULE, type NuxtHooks } from "./nuxt-meta.ts";
import type { RunWispOptions } from "./runwisp.ts";

/**
 * The module's options: `new RunWisp()` options, the ones that can be written
 * into the server build. The password stays out of the build: set
 * `RUNWISP_PASSWORD` where the server runs.
 */
export type RunWispNuxtOptions = Omit<RunWispOptions, "log" | "exitOnSignal" | "password">;

interface NuxtLike {
    options: {
        dev: boolean;
        buildDir: string;
        build: { templates: { filename: string; getContents: () => string }[] };
        runwisp?: RunWispNuxtOptions | false | undefined;
    };
    hook<K extends keyof NuxtHooks>(name: K, fn: NuxtHooks[K]): void;
}

const here = fileURLToPath(import.meta.url);
const plugin = (name: string) => join(dirname(here), `${name}${extname(here)}`);

// The Nitro entries that start a server and keep it running, under Node or
// Bun: node-server is also what most host presets (heroku, render-com) use.
const SERVER_ENTRIES = new Set(["node-server", "node-cluster", "node-listener", "bun"]);

export default function runwispNuxt(inlineOptions: RunWispNuxtOptions, nuxt: NuxtLike): void {
    // `runwisp: false` turns a Nuxt module off.
    if (nuxt.options.runwisp === false) return;
    const { config: runwispConfig, ...options } = { ...nuxt.options.runwisp, ...inlineOptions };
    // The bun preset's server exits on SIGTERM without closing Nitro, so the
    // close hook never runs: the plugin handles the signal itself there.
    let exitOnSignal = false;
    nuxt.hook("nitro:init", (nitro) => {
        const { preset, entry } = nitro.options;
        exitOnSignal = preset === "bun";
        if (nuxt.options.dev || (entry !== undefined && SERVER_ENTRIES.has(parse(entry).name))) {
            return;
        }
        // Serverless, edge and static builds have no process for the tasks to run in.
        throw new Error(
            `@runwisp/js/nuxt: tasks need a server that keeps running, and the "${preset}" preset doesn't start one. Build with the node-server or bun preset, or remove the module.`,
        );
    });
    // Its own file: Nitro's types file has imports, which would turn the
    // declaration into an augmentation of a module that doesn't exist.
    const types = { filename: "types/runwisp-nuxt.d.ts", getContents: () => TYPES };
    nuxt.options.build.templates.push(types);
    nuxt.hook("nitro:prepare:types", ({ references }) => {
        references.push({ path: join(nuxt.options.buildDir, types.filename) });
    });
    nuxt.hook("nitro:config", (config) => {
        // Nitro allows one task under several schedules; RunWisp has one per task.
        const scheduled: [cron: string, task: string][] = Object.entries(
            config.scheduledTasks ?? {},
        ).flatMap(([cron, tasks]) => [tasks].flat().map((task): [string, string] => [cron, task]));
        delete config.scheduledTasks;

        config.virtual = {
            ...config.virtual,
            [VIRTUAL_MODULE]: () => moduleSource({ options }),
            // Read when Nitro bundles the plugins, by which time it has picked the preset.
            [INTERNAL_MODULE]: () =>
                moduleSource({
                    dev: nuxt.options.dev,
                    exitOnSignal,
                    scheduled,
                    config: runwispConfig,
                }),
        };
        config.plugins = [
            ...(config.plugins ?? []),
            plugin("nuxt-close"),
            ...(scheduled.length > 0 ? [plugin("nuxt-tasks")] : []),
        ];
        config.externals = {
            ...config.externals,
            // Nuxt inlines module packages, but inlined, @runwisp/js can't
            // find the runwisp binary. Only the plugins need bundling, for
            // their virtual imports; they import @runwisp/js by name, so
            // they share the app's instance.
            inline: [
                ...(config.externals?.inline ?? []).filter((id) => id !== "@runwisp/js"),
                plugin("nuxt-close"),
                plugin("nuxt-tasks"),
            ],
            traceInclude: [...(config.externals?.traceInclude ?? []), ...binaryFiles()],
        };
    });
}

function moduleSource(values: Record<string, unknown>): string {
    return Object.entries(values)
        .map(([key, value]) => `export const ${key} = ${JSON.stringify(value)};`)
        .join("\n");
}

// Nuxt reads the config key from here, and types the `runwisp` key with it.
runwispNuxt.getMeta = () => Promise.resolve({ name: "@runwisp/js/nuxt", configKey: "runwisp" });
