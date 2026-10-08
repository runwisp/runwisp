// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { createServer } from "node:net";

import { call, field, resolveBinary } from "../src/daemon.ts";

// The freshly built binary in CI, else the one from the runwisp npm package.
export const binary = resolveBinary(process.env["RUNWISP_NODE_TEST_BINARY"]);

export function freePort(): Promise<number> {
    return new Promise((resolve, reject) => {
        const server = createServer();
        server.on("error", reject);
        server.listen(0, "127.0.0.1", () => {
            const address = server.address();
            server.close(() => {
                resolve(typeof address === "object" && address ? address.port : 0);
            });
        });
    });
}

/** Runs a task through the daemon, waits for it, and returns its exit code and log. */
export async function trigger(
    socket: string,
    task: string,
): Promise<{ exitCode: unknown; log: unknown }> {
    const run = await call(socket, "POST", `/api/tasks/${task}/run?wait=true`);
    const id = String(field(run.body, "id"));
    const raw = await call(socket, "GET", `/api/runs/${id}/log/raw`);
    return { exitCode: field(run.body, "exitCode"), log: raw.body };
}
