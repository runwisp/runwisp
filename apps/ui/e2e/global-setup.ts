// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { FullConfig } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./fixtures/daemon-state.js";
import { generatePassword, obtainToken, waitForHealth } from "./fixtures/daemon-boot.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, ".state.json");

async function globalSetup(_config: FullConfig): Promise<void> {
    const port = Number(process.env.E2E_PORT) || 19287;
    const runnerRoot = resolve(__dirname, "../../runwisp");
    const binaryPath = join(runnerRoot, "runwisp");
    const configPath = resolve(__dirname, "fixtures/runwisp.e2e.toml");
    const dataDir = await mkdtemp(join(tmpdir(), "runwisp-e2e-"));
    const password = generatePassword();

    const daemon = spawn(
        binaryPath,
        ["--config", configPath, "--data", dataDir, "--port", String(port), "daemon"],
        {
            cwd: runnerRoot,
            stdio: ["ignore", "pipe", "pipe"],
            detached: true,
            env: { ...process.env, RUNWISP_PASSWORD: password },
        },
    );

    let daemonOutput = "";
    daemon.stdout?.on("data", (chunk: Buffer) => {
        daemonOutput += chunk.toString();
    });
    daemon.stderr?.on("data", (chunk: Buffer) => {
        daemonOutput += chunk.toString();
    });

    daemon.on("error", (err) => {
        console.error("[e2e] daemon spawn error:", err);
    });

    daemon.on("exit", (code, signal) => {
        console.error(`[e2e] daemon exited: code=${code} signal=${signal}`);
        if (daemonOutput) console.error("[e2e] daemon output:\n", daemonOutput);
    });

    daemon.unref();

    const baseURL = `http://127.0.0.1:${port}`;

    console.log(`[e2e] Starting daemon: ${binaryPath}`);
    console.log(`[e2e] Config: ${configPath}`);
    console.log(`[e2e] Data dir: ${dataDir}`);
    console.log(`[e2e] Port: ${port}`);
    console.log(`[e2e] PID: ${daemon.pid}`);

    await waitForHealth(baseURL, 15_000);

    // Obtain a JWT once to avoid rate-limit issues across tests
    const token = await obtainToken(baseURL, password);

    if (daemon.pid === undefined) throw new Error("Daemon process has no PID");
    const state: DaemonState = {
        pid: daemon.pid,
        port,
        dataDir,
        password,
        token,
    };
    await writeFile(STATE_PATH, JSON.stringify(state));
}

export default globalSetup;
