// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import type { FullConfig } from "@playwright/test";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import type { DaemonState } from "./fixtures/daemon-state.js";

const __dirname = dirname(fileURLToPath(import.meta.url));
const STATE_PATH = resolve(__dirname, ".state.json");

async function globalSetup(_config: FullConfig): Promise<void> {
    const port = Number(process.env.E2E_PORT) || 19287;
    const runnerRoot = resolve(__dirname, "../../runwisp");
    const binaryPath = join(runnerRoot, "runwisp");
    const configPath = resolve(__dirname, "fixtures/runwisp.e2e.yaml");
    const dataDir = await mkdtemp(join(tmpdir(), "runwisp-e2e-"));

    const daemon = spawn(
        binaryPath,
        ["--config", configPath, "--data", dataDir, "--port", String(port), "daemon"],
        {
            cwd: runnerRoot,
            stdio: ["ignore", "pipe", "pipe"],
            detached: true,
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

    const password = await waitForPassword(dataDir, 10_000);

    // Obtain a JWT once to avoid rate-limit issues across tests
    const token = await obtainToken(baseURL, password);

    const state: DaemonState = {
        pid: daemon.pid!,
        port,
        dataDir,
        password,
        token,
    };
    await writeFile(STATE_PATH, JSON.stringify(state));
}

async function obtainToken(baseURL: string, password: string): Promise<string> {
    const challengeRes = await fetch(`${baseURL}/api/auth/challenge`);
    if (!challengeRes.ok) throw new Error(`Challenge request failed: ${challengeRes.status}`);
    const { nonce } = (await challengeRes.json()) as { nonce: string };

    const enc = new TextEncoder();
    const hashBuf = await globalThis.crypto.subtle.digest(
        "SHA-256",
        enc.encode(`${password}:${nonce}`),
    );
    const response = Array.from(new Uint8Array(hashBuf))
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");

    const authRes = await fetch(`${baseURL}/api/auth`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nonce, response }),
    });
    if (!authRes.ok) throw new Error(`Auth request failed: ${authRes.status}`);
    const data = (await authRes.json()) as { token: string };
    return data.token;
}

async function waitForHealth(baseURL: string, timeout: number): Promise<void> {
    const deadline = Date.now() + timeout;

    while (Date.now() < deadline) {
        try {
            const res = await fetch(`${baseURL}/health`);
            if (res.ok) return;
        } catch {
            // daemon not ready yet
        }
        await sleep(100);
    }

    throw new Error(`Daemon did not become healthy within ${timeout}ms at ${baseURL}`);
}

async function waitForPassword(dataDir: string, timeout: number): Promise<string> {
    const passwordPath = join(dataDir, "password");
    const deadline = Date.now() + timeout;

    while (Date.now() < deadline) {
        try {
            const content = await readFile(passwordPath, "utf-8");
            const password = content.trim();
            if (password) return password;
        } catch {
            // file not created yet
        }
        await sleep(100);
    }

    throw new Error(`Password file not created within ${timeout}ms at ${passwordPath}`);
}

function sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

export default globalSetup;
