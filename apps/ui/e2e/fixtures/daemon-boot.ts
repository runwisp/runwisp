// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Shared daemon-boot helpers used by both the e2e harness (global-setup.ts) and
// the docs screenshot harness (screenshots/global-setup.ts). Both spawn the real
// binary, wait for it to come up, and exchange the password for a session token via the
// challenge-response handshake — identical steps, one source of truth.

import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { chapResponse } from "../../src/lib/chap";

// The daemon must die with the Playwright runner, even when the runner is
// SIGKILLed and global-teardown never runs; otherwise the orphan keeps the port
// and every later run fails. Node can't set a parent-death signal, so this sh
// wrapper holds a pipe from the runner on fd 3: when the runner dies the pipe
// hits EOF and the watcher kills the daemon. The wrapper exits with the
// daemon's own status and leads the process group that teardown signals.
const LIFELINE = `exec 3<&0
"$0" "$@" 3<&- </dev/null &
pid=$!
(read _ <&3; kill $pid) 2>/dev/null &
wait $pid; code=$?
kill $! 2>/dev/null
exit $code`;

/**
 * The daemon port for this run: `$envVar` when set, otherwise a free port from
 * the OS, so concurrent runs (other worktrees, a manual daemon) never collide.
 * The choice is written back to the env: the config calls this first in the
 * runner, and Playwright workers re-load the config with the inherited env, so
 * config, globalSetup, and every worker agree on one port.
 */
export async function runPort(envVar: string): Promise<number> {
    process.env[envVar] ||= String(await freePort());
    return Number(process.env[envVar]);
}

// ponytail: the port is free when probed, not reserved; another process could
// grab it before the daemon binds. The OS doesn't hand the same ephemeral port
// out twice in a row, and startDaemon's busy-port check catches the rest.
function freePort(): Promise<number> {
    return new Promise((resolvePort, reject) => {
        const server = createServer();
        server.on("error", reject);
        server.listen(0, "127.0.0.1", () => {
            const address = server.address();
            if (!address || typeof address === "string") {
                reject(new Error("TCP listener has no port"));
                return;
            }
            server.close(() => resolvePort(address.port));
        });
    });
}

export interface DaemonSpawn {
    binaryPath: string;
    args: string[];
    cwd: string;
    port: number;
    password: string;
    /** Log prefix, e.g. "e2e". */
    label: string;
}

/**
 * Spawn the daemon tied to this process's lifetime, wait until it's healthy,
 * and return the process-group PID global-teardown signals.
 */
export async function startDaemon(opts: DaemonSpawn): Promise<number> {
    const baseURL = `http://127.0.0.1:${opts.port}`;
    // Anything already answering is not ours (another worktree's run, a manual
    // daemon). Fail loudly instead of silently testing against it.
    if (
        await fetch(`${baseURL}/health`).then(
            () => true,
            () => false,
        )
    ) {
        throw new Error(
            `Port ${opts.port} already serves a daemon; stop it or unset the port override`,
        );
    }

    const daemon = spawn("sh", ["-c", LIFELINE, opts.binaryPath, ...opts.args], {
        cwd: opts.cwd,
        stdio: ["pipe", "pipe", "pipe"],
        detached: true,
        env: { ...process.env, RUNWISP_PASSWORD: opts.password },
    });

    let output = "";
    daemon.stdout?.on("data", (chunk: Buffer) => (output += chunk.toString()));
    daemon.stderr?.on("data", (chunk: Buffer) => (output += chunk.toString()));
    daemon.on("error", (err) => console.error(`[${opts.label}] daemon spawn error:`, err));
    daemon.on("exit", (code, signal) => {
        console.error(`[${opts.label}] daemon exited: code=${code} signal=${signal}`);
        if (output) console.error(`[${opts.label}] daemon output:\n`, output);
    });
    daemon.unref();

    if (daemon.pid === undefined) throw new Error("Daemon process has no PID");
    console.log(`[${opts.label}] PID: ${daemon.pid}`);
    await waitForHealth(baseURL, 15_000);
    return daemon.pid;
}

export function generatePassword(): string {
    const bytes = new Uint8Array(24);
    globalThis.crypto.getRandomValues(bytes);
    return Array.from(bytes)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

export function sleep(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function waitForHealth(baseURL: string, timeout: number): Promise<void> {
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

/** Run the challenge-response handshake and return a session token. */
export async function obtainToken(baseURL: string, password: string): Promise<string> {
    const challengeRes = await fetch(`${baseURL}/api/auth/challenge`);
    if (!challengeRes.ok) throw new Error(`Challenge request failed: ${challengeRes.status}`);
    const { nonce } = (await challengeRes.json()) as { nonce: string };

    const response = await chapResponse(password, nonce);

    const authRes = await fetch(`${baseURL}/api/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ nonce, response }),
    });
    if (!authRes.ok) throw new Error(`Auth request failed: ${authRes.status}`);
    const data = (await authRes.json()) as { token: string };
    return data.token;
}
