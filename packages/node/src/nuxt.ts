// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A Nuxt module: `modules: ["@runwisp/node/nuxt"]`. Ships the runwisp binary
// with the build, closes RunWisp when the server stops, and runs Nitro's
// `scheduledTasks` under RunWisp instead of Nitro's own scheduler.

import { dirname, extname, join, parse } from "node:path";
import { fileURLToPath } from "node:url";

import { binaryFiles } from "./daemon.ts";
import type { RunWispOptions } from "./runwisp.ts";

/**
 * The module's options: `new RunWisp()` options, the ones that can be written
 * into the server build. The password stays out of the build: set
 * `RUNWISP_PASSWORD` where the server runs.
 */
export type RunWispNuxtOptions = Omit<RunWispOptions, "log" | "exitOnSignal" | "password">;

/** The part of Nitro's config this module changes. */
export interface NitroConfig {
    scheduledTasks?: Record<string, string | string[]>;
    plugins?: string[];
    virtual?: Record<string, string | (() => string | Promise<string>)>;
    externals?: { inline?: (string | RegExp)[]; traceInclude?: string[] };
}

/** The Nuxt hooks this module uses. */
export interface NuxtHooks {
    "nitro:config": (config: NitroConfig) => void;
    "nitro:init": (nitro: { options: { preset: string; entry?: string | undefined } }) => void;
    "nitro:prepare:types": (types: { declarations: string[] }) => void;
}

export interface NuxtLike {
    options: { dev: boolean };
    hook<K extends keyof NuxtHooks>(name: K, fn: NuxtHooks[K]): void;
}

/** What the module tells its Nitro plugins, as a virtual module. */
export const VIRTUAL_MODULE = "#runwisp/nuxt";

const here = fileURLToPath(import.meta.url);
const plugin = (name: string) => join(dirname(here), `${name}${extname(here)}`);

// The Nitro entries that start a server and keep it running, under Node or
// Bun: node-server is also what most host presets (heroku, render-com) use.
const SERVER_ENTRIES = new Set(["node-server", "node-cluster", "node-listener", "bun"]);

export default function runwispNuxt(moduleOptions: RunWispNuxtOptions, nuxt: NuxtLike): void {
    const { config: runwispConfig, ...options } = moduleOptions;
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
            `@runwisp/node/nuxt: tasks need a server that keeps running, and the "${preset}" preset doesn't start one. Build with the node-server or bun preset, or remove the module.`,
        );
    });
    nuxt.hook("nitro:prepare:types", ({ declarations }) => {
        declarations.push(
            `declare module "${VIRTUAL_MODULE}" { export const options: import("@runwisp/node").RunWispOptions; }`,
        );
    });
    nuxt.hook("nitro:config", (config) => {
        // Nitro allows one task under several schedules; RunWisp has one per task.
        const scheduled: [cron: string, task: string][] = Object.entries(
            config.scheduledTasks ?? {},
        ).flatMap(([cron, tasks]) => [tasks].flat().map((task): [string, string] => [cron, task]));
        delete config.scheduledTasks;

        config.virtual = {
            ...config.virtual,
            // Read when Nitro bundles the plugins, by which time it has picked the preset.
            [VIRTUAL_MODULE]: () =>
                Object.entries({
                    dev: nuxt.options.dev,
                    exitOnSignal,
                    scheduled,
                    options,
                    config: runwispConfig,
                })
                    .map(([key, value]) => `export const ${key} = ${JSON.stringify(value)};`)
                    .join("\n"),
        };
        config.plugins = [
            ...(config.plugins ?? []),
            plugin("nuxt-close"),
            ...(scheduled.length > 0 ? [plugin("nuxt-tasks")] : []),
        ];
        config.externals = {
            ...config.externals,
            // Nuxt inlines module packages, but inlined, @runwisp/node can't
            // find the runwisp binary. Only the plugins need bundling, for
            // their virtual imports; they import @runwisp/node by name, so
            // they share the app's instance.
            inline: [
                ...(config.externals?.inline ?? []).filter((id) => id !== "@runwisp/node"),
                plugin("nuxt-close"),
                plugin("nuxt-tasks"),
            ],
            traceInclude: [...(config.externals?.traceInclude ?? []), ...binaryFiles()],
        };
    });
}
