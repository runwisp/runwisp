// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Shared daemon-boot helpers used by both the e2e harness (global-setup.ts) and
// the docs screenshot harness (screenshots/global-setup.ts). Both spawn the real
// binary, wait for it to come up, and exchange the password for a JWT via the
// challenge-response handshake — identical steps, one source of truth.

import { chapResponse } from "../../src/lib/chap";

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

/** Run the challenge-response handshake and return a session JWT. */
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
