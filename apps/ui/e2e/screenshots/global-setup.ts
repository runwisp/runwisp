// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Boots a demo-seeded daemon for the docs screenshot run. Unlike the e2e
// harness (which uses a tiny fixtures/runwisp.e2e.toml), this one seeds the rich
// "Acme Notes" demo config — hundreds of believable historical runs — so the
// captured Web UI looks like a daemon that's been running for weeks.
//
// Two-step boot, both using the real binary:
//   1. `runwisp demo --seed-only` writes the embedded demo config and seeds the
//      data dir, then exits (no daemon, no TUI).
//   2. start `runwisp daemon` against that config/data via the same
//      startDaemon helper as the e2e setup, and hand the JWT to the specs via
//      .state.json.
//
// It writes the same .state.json the e2e harness uses, so the screenshot config
// reuses fixtures/test-base.ts (authenticatedPage) and global-teardown.ts.

import type { FullConfig } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "../fixtures/daemon-state.js";
import { generatePassword, obtainToken, runPort, startDaemon } from "../fixtures/daemon-boot.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, "../.state.json");

async function globalSetup(_config: FullConfig): Promise<void> {
    const port = await runPort("SCREENSHOT_PORT");
    const runnerRoot = resolve(__dirname, "../../../runwisp");
    const binaryPath = join(runnerRoot, "runwisp");
    const dataDir = await mkdtemp(join(tmpdir(), "runwisp-screenshots-"));
    const configPath = join(dataDir, "runwisp.toml");
    const password = generatePassword();

    console.log(`[screenshots] Binary: ${binaryPath}`);
    console.log(`[screenshots] Data dir: ${dataDir}`);
    console.log(`[screenshots] Port: ${port}`);

    await seedDemoData(binaryPath, runnerRoot, configPath, dataDir);

    const pid = await startDaemon({
        binaryPath,
        args: ["--config", configPath, "--data", dataDir, "--port", String(port), "daemon"],
        cwd: runnerRoot,
        port,
        password,
        label: "screenshots",
    });
    const baseURL = `http://127.0.0.1:${port}`;
    const token = await obtainToken(baseURL, password);

    const state: DaemonState = { pid, port, dataDir, password, token };
    await writeFile(STATE_PATH, JSON.stringify(state));
}

/** Run `runwisp demo --seed-only` to disk and resolve once it exits cleanly. */
function seedDemoData(
    binaryPath: string,
    cwd: string,
    configPath: string,
    dataDir: string,
): Promise<void> {
    return new Promise((resolvePromise, reject) => {
        console.log("[screenshots] Seeding demo history (a few seconds)...");
        const seed = spawn(
            binaryPath,
            ["--config", configPath, "--data", dataDir, "demo", "--seed-only"],
            { cwd, stdio: ["ignore", "pipe", "pipe"] },
        );

        let output = "";
        seed.stdout?.on("data", (chunk: Buffer) => (output += chunk.toString()));
        seed.stderr?.on("data", (chunk: Buffer) => (output += chunk.toString()));
        seed.on("error", reject);
        seed.on("exit", (code) => {
            if (code === 0) {
                resolvePromise();
                return;
            }
            reject(new Error(`demo --seed-only exited with code ${code}\n${output}`));
        });
    });
}

export default globalSetup;
