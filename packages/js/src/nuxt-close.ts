// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Nitro plugin added by the Nuxt module, ahead of the app's plugins: hands
// the module's config to the first RunWisp, and when the server stops, lets
// running tasks finish, then stops RunWisp.

import cluster from "node:cluster";
import { constants } from "node:os";
import { join } from "node:path";

import { options } from "#runwisp/nuxt";
import { clustered, config, dev, exitOnSignal, output, rootDir } from "#runwisp/nuxt-internal";

import { RunWisp } from "@runwisp/js";

export interface NitroAppLike {
    hooks: { hook(name: "close", fn: () => Promise<void>): void };
}

export default function (nitroApp: NitroAppLike): void {
    closeRunWisp(nitroApp, import.meta.prerender);
}

const SIGNALS = ["SIGTERM", "SIGINT"] as const;

export function closeRunWisp(nitroApp: NitroAppLike, prerender: boolean): void {
    // `nuxi build` runs the plugins to prerender routes.
    if (prerender) return;
    // `nuxi preview` runs the server from inside the build, which the next
    // build deletes: keep the data in the project, as `nuxi dev` does.
    if (!options.data && !process.env["RUNWISP_DATA"] && output && process.cwd() === output) {
        process.env["RUNWISP_DATA"] = join(rootDir, ".runwisp");
    }
    // Only the first RunWisp for a data directory takes config; the app's own
    // `new RunWisp(options)` then gets this one.
    if (config) new RunWisp({ ...options, config });
    // Dev reloads the server on every edit; RunWisp keeps running across them.
    if (dev) return;
    if (clustered) {
        closeCluster(nitroApp);
        return;
    }
    nitroApp.hooks.hook("close", () => RunWisp.closeAll());
    if (!exitOnSignal) return;
    for (const signal of SIGNALS) {
        // Exits as a process killed by the signal would, like exitOnSignal.
        process.once(signal, () => {
            void RunWisp.closeAll().finally(() => process.exit(128 + constants.signals[signal]));
        });
    }
}

/** What a message between the cluster's primary and its workers says: its `runwisp` key, and a `signal`. */
function messageKey(message: unknown, key: string): unknown {
    return typeof message === "object" && message !== null && key in message
        ? Object.getOwnPropertyDescriptor(message, key)?.value
        : undefined;
}

/**
 * The node-cluster entry: the primary runs the app too, but on SIGTERM only
 * waits for its workers, never closing Nitro, so its tasks would keep firing
 * until Nitro gave up after 30 seconds and killed the runs. The primary
 * closes its RunWisp itself, tells the workers to stop, and the workers
 * exit once it is done, which is what ends the primary.
 */
function closeCluster(nitroApp: NitroAppLike): void {
    const tell = (message: { runwisp: "stop"; signal: string } | { runwisp: "closed" }) => {
        for (const worker of Object.values(cluster.workers ?? {})) {
            if (worker?.isConnected() === true) worker.send(message, () => undefined);
        }
    };
    if (cluster.isPrimary) {
        for (const signal of SIGNALS) {
            process.once(signal, () => {
                tell({ runwisp: "stop", signal });
                void RunWisp.closeAll().finally(() => {
                    tell({ runwisp: "closed" });
                });
            });
        }
        return;
    }
    // Nitro's worker stops on the signal alone, which only the primary got
    // when something like `docker stop` signals it; and with a signal to the
    // whole group, a worker may be done before the primary is.
    const primaryClosed = new Promise<void>((resolve) => {
        process.once("disconnect", resolve);
        process.on("message", (message) => {
            const key = messageKey(message, "runwisp");
            if (key === "closed") resolve();
            const signal = messageKey(message, "signal");
            if (key === "stop" && typeof signal === "string") process.emit(signal);
        });
    });
    nitroApp.hooks.hook("close", async () => {
        await RunWisp.closeAll();
        await primaryClosed;
    });
}
