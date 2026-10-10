// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// One copy of an app that uses @runwisp/node, for the e2e tests:
// `node app.ts <data> <port> <binary>`. Prints "ready" once RunWisp serves its
// tasks, and closes on a "close" line on stdin. RUNWISP_TEST_APP names it.

import { createInterface } from "node:readline";
import { setTimeout as sleep } from "node:timers/promises";

import { RunWisp } from "../../src/index.ts";

const [data, port, binary] = process.argv.slice(2);
if (!data || !port || !binary) throw new Error("usage: app.ts <data> <port> <binary>");

const app = process.env["RUNWISP_TEST_APP"];
const cron = new RunWisp({
    data,
    port: Number(port),
    binary,
    log: false,
    ...(app === undefined ? {} : { app }),
    // Runs in the daemon, so it sees the daemon's env, not this copy's.
    config: { tasks: { "shell-env": { run: 'echo "foo=$FOO"' } } },
});

cron.task("who", {}, () => {
    console.log(`pid ${String(process.pid)}`);
});

// The first run ignores ctx.signal and outlives its timeout.
const stubborn = { runs: 0 };
cron.task("stubborn", { on_overlap: "skip", timeout: "1s", graceful_stop: "3s" }, async () => {
    if (++stubborn.runs === 1) await sleep(10_000);
});

cron.task("crash", {}, async () => {
    setTimeout(() => {
        throw new Error("kaboom");
    }, 50);
    await sleep(60_000);
});

await cron.start();
process.stdout.write("ready\n");

createInterface({ input: process.stdin }).on("line", (line) => {
    if (line === "close") void cron.close().then(() => process.exit(0));
});
