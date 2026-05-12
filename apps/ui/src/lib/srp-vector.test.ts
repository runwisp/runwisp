// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

import { describe, expect, it } from "vitest";
import { Triplet, RFC5054Group4096, type Params } from "@mzattahri/srp";
import vector from "../../../../packages/common/src/srp-test-vector.json" with { type: "json" };

// Node 18+ exposes WebCrypto on globalThis.crypto/crypto.subtle by default,
// which is all the SRP params below need. If a future Node downgrade lands
// in CI, surface that explicitly so the cause is obvious.
if (typeof globalThis.crypto.subtle === "undefined") {
    throw new Error("globalThis.crypto.subtle is unavailable — needs Node 18+");
}

function concatU8(...parts: Uint8Array[]): Uint8Array {
    const len = parts.reduce((n, p) => n + p.length, 0);
    const out = new Uint8Array(len);
    let off = 0;
    for (const p of parts) {
        out.set(p, off);
        off += p.length;
    }
    return out;
}

function hexToU8(hex: string): Uint8Array {
    const out = new Uint8Array(hex.length / 2);
    for (let i = 0; i < out.length; i++) {
        out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    }
    return out;
}

function u8ToHex(buf: Uint8Array): string {
    return Array.from(buf)
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}

const srpParams: Params = {
    name: "DH16-SHA256-PBKDF2-600k",
    group: RFC5054Group4096,
    hash: async (...inputs: Uint8Array[]) => {
        const data = concatU8(...inputs);
        const buf = new ArrayBuffer(data.byteLength);
        new Uint8Array(buf).set(data);
        return new Uint8Array(await crypto.subtle.digest("SHA-256", buf));
    },
    kdf: async (_username: string, password: string, salt: Uint8Array) => {
        const pwBytes = new TextEncoder().encode(password);
        const pwBuf = new ArrayBuffer(pwBytes.byteLength);
        new Uint8Array(pwBuf).set(pwBytes);
        const passwordKey = await crypto.subtle.importKey("raw", pwBuf, { name: "PBKDF2" }, false, [
            "deriveBits",
        ]);
        const saltBuf = new ArrayBuffer(salt.byteLength);
        new Uint8Array(saltBuf).set(salt);
        const bits = await crypto.subtle.deriveBits(
            {
                name: "PBKDF2",
                hash: "SHA-256",
                salt: saltBuf,
                iterations: vector.iterations,
            },
            passwordKey,
            32 * 8,
        );
        return new Uint8Array(bits);
    },
};

describe("SRP drift vector (Go ↔ TS)", () => {
    it("produces the verifier the Go side computes for the same inputs", async () => {
        const salt = hexToU8(vector.salt_hex);
        const triplet = await Triplet.create(srpParams, vector.identity, vector.password, salt);
        expect(u8ToHex(triplet.verifier)).toBe(vector.expected_verifier_hex);
    }, 30_000);
});
