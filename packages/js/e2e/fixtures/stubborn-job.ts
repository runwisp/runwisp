// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// A node-cron background task file, for the e2e tests: starts a child, then
// ignores SIGTERM, so only a SIGKILL to its process group ends them both.

import { spawn } from "node:child_process";

export function task(): Promise<never> {
    process.on("SIGTERM", () => {
        console.log("ignoring SIGTERM");
    });
    const sleeper = spawn("sleep", ["60"], { stdio: "ignore" });
    console.log(`sleeper ${String(sleeper.pid)}`);
    return new Promise(() => {
        setInterval(() => undefined, 1000);
    });
}
