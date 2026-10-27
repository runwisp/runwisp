// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// What Nitro provides when it bundles the Nuxt module's plugins.

declare module "#imports" {
    export function runTask(
        name: string,
        options: { payload: Record<string, unknown> },
    ): Promise<unknown>;
}

declare module "#runwisp/nuxt" {
    import type { RunWispOptions } from "@runwisp/js";

    /** The module's options, without config: what every `new RunWisp()` in the app passes. */
    export const options: RunWispOptions;
}

declare module "#runwisp/nuxt-internal" {
    import type { Config } from "@runwisp/js";

    export const dev: boolean;
    export const exitOnSignal: boolean;
    export const clustered: boolean;
    export const scheduled: [cron: string, task: string][];
    export const config: Config | undefined;
    export const output: string | undefined;
    export const rootDir: string;
}

interface ImportMeta {
    /** true while Nitro prerenders routes during the build. */
    readonly prerender: boolean;
}
