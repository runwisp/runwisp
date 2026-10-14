// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Nitro plugin added by the Nuxt module, ahead of the app's plugins: hands
// the module's config to the first RunWisp, and when the server stops, lets
// running tasks finish, then stops RunWisp.

import { constants } from "node:os";

import { options } from "#runwisp/nuxt";
import { config, dev, exitOnSignal } from "#runwisp/nuxt-internal";

import { RunWisp } from "@runwisp/js";

export interface NitroAppLike {
    hooks: { hook(name: "close", fn: () => Promise<void>): void };
}

export default function (nitroApp: NitroAppLike): void {
    closeRunWisp(nitroApp, import.meta.prerender);
}

export function closeRunWisp(nitroApp: NitroAppLike, prerender: boolean): void {
    // `nuxi build` runs the plugins to prerender routes.
    if (prerender) return;
    // Only the first RunWisp for a data directory takes config; the app's own
    // `new RunWisp(options)` then gets this one.
    if (config) new RunWisp({ ...options, config });
    // Dev reloads the server on every edit; RunWisp keeps running across them.
    if (dev) return;
    nitroApp.hooks.hook("close", () => RunWisp.closeAll());
    if (!exitOnSignal) return;
    for (const signal of ["SIGTERM", "SIGINT"] as const) {
        // Exits as a process killed by the signal would, like exitOnSignal.
        process.once(signal, () => {
            void RunWisp.closeAll().finally(() => process.exit(128 + constants.signals[signal]));
        });
    }
}
