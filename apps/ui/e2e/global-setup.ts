// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

import type { FullConfig } from "@playwright/test";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./fixtures/daemon-state.js";
import { generatePassword, obtainToken, runPort, startDaemon } from "./fixtures/daemon-boot.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, ".state.json");

async function globalSetup(_config: FullConfig): Promise<void> {
    const port = await runPort("E2E_PORT");
    const runnerRoot = resolve(__dirname, "../../runwisp");
    const binaryPath = join(runnerRoot, "runwisp");
    const configPath = resolve(__dirname, "fixtures/runwisp.e2e.toml");
    const dataDir = await mkdtemp(join(tmpdir(), "runwisp-e2e-"));
    const password = generatePassword();

    console.log(`[e2e] Starting daemon: ${binaryPath}`);
    console.log(`[e2e] Config: ${configPath}`);
    console.log(`[e2e] Data dir: ${dataDir}`);
    console.log(`[e2e] Port: ${port}`);

    const pid = await startDaemon({
        binaryPath,
        args: ["--config", configPath, "--data", dataDir, "--port", String(port), "daemon"],
        cwd: runnerRoot,
        port,
        password,
        label: "e2e",
    });
    const baseURL = `http://127.0.0.1:${port}`;

    // Obtain a session token once to avoid rate-limit issues across tests
    const token = await obtainToken(baseURL, password);

    const state: DaemonState = {
        pid,
        port,
        dataDir,
        password,
        token,
    };
    await writeFile(STATE_PATH, JSON.stringify(state));
}

export default globalSetup;
