// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { readFile, rm, unlink } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./fixtures/daemon-state.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, ".state.json");

async function globalTeardown(): Promise<void> {
    let state: DaemonState | undefined;

    try {
        const raw = await readFile(STATE_PATH, "utf-8");
        state = JSON.parse(raw) as DaemonState;
    } catch {
        return;
    }

    if (state?.pid) {
        try {
            process.kill(-state.pid, "SIGINT");
        } catch {
            // Process may already be gone
        }

        await sleep(2_000);

        try {
            process.kill(-state.pid, "SIGKILL");
        } catch {
            // Already exited
        }
    }

    await safeUnlink(STATE_PATH);

    if (state?.dataDir) {
        await rm(state.dataDir, { recursive: true, force: true });
    }
}

function sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

async function safeUnlink(path: string): Promise<void> {
    try {
        await unlink(path);
    } catch {
        // File may not exist
    }
}

export default globalTeardown;
