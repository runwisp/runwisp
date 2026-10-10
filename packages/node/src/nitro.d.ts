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
    import type { Config, RunWispOptions } from "@runwisp/node";

    export const dev: boolean;
    export const exitOnSignal: boolean;
    export const scheduled: [cron: string, task: string][];
    /** The module's options, without config: what every `new RunWisp()` in the app passes. */
    export const options: RunWispOptions;
    export const config: Config | undefined;
}

interface ImportMeta {
    /** true while Nitro prerenders routes during the build. */
    readonly prerender: boolean;
}
