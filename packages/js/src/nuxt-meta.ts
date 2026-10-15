// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// The virtual modules the Nuxt module adds to the Nitro build, and the part
// of Nuxt it uses. Not a package export: only nuxt.ts and its tests use these.

/** What the app's server code imports: the module's options. */
export const VIRTUAL_MODULE = "#runwisp/nuxt";

/** What only the module's own Nitro plugins import. */
export const INTERNAL_MODULE = "#runwisp/nuxt-internal";

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
    "nitro:prepare:types": (types: { references: { path: string }[] }) => void;
}

/** The app-facing virtual module's types, for the app's server code. */
export const TYPES = `declare module "${VIRTUAL_MODULE}" { export const options: import("@runwisp/js").RunWispOptions; }\n`;
