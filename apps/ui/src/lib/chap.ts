// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// chapResponse mirrors chap.Response in Go
// (apps/runwisp/internal/chap/chap.go): the daemon's challenge-response
// computation, which the browser, the Go CLI, and the Go server must all agree
// on byte-for-byte. The shared test vectors in chap.test.ts and the Go
// chap_test.go guard the two implementations against drift.
//
// The formula is hex(PBKDF2-HMAC-SHA256(password, salt=nonce, iterations, 32
// bytes)). PBKDF2 makes an intercepted login transcript expensive to
// brute-force offline — defense in depth behind TLS, not a substitute for it.
//
// Browsers only expose crypto.subtle in secure contexts (HTTPS or localhost),
// so a daemon reached over plain HTTP at a LAN address falls back to the
// pure-JS @noble/hashes PBKDF2. It's slower, so it's loaded only when needed.

// CHAP_PBKDF2_ITERATIONS MUST equal the Go constant chap.Iterations. A bump
// here is only correct if the Go side and the test vectors move with it.
const CHAP_PBKDF2_ITERATIONS = 600_000;

// DERIVED_BITS is the output size: 256 bits = 32 bytes = the SHA-256 width,
// matching the Go keyLength.
const DERIVED_BITS = 256;

export async function chapResponse(password: string, nonce: string): Promise<string> {
    // lib.dom types subtle as always present; insecure contexts omit it.
    if (!("subtle" in globalThis.crypto)) {
        const [{ pbkdf2Async }, { sha256 }, { bytesToHex }] = await Promise.all([
            import("@noble/hashes/pbkdf2.js"),
            import("@noble/hashes/sha2.js"),
            import("@noble/hashes/utils.js"),
        ]);
        const key = await pbkdf2Async(sha256, password, nonce, {
            c: CHAP_PBKDF2_ITERATIONS,
            dkLen: DERIVED_BITS / 8,
        });
        return bytesToHex(key);
    }
    const enc = new TextEncoder();
    const keyMaterial = await globalThis.crypto.subtle.importKey(
        "raw",
        enc.encode(password),
        "PBKDF2",
        false,
        ["deriveBits"],
    );
    const bits = await globalThis.crypto.subtle.deriveBits(
        {
            name: "PBKDF2",
            salt: enc.encode(nonce),
            iterations: CHAP_PBKDF2_ITERATIONS,
            hash: "SHA-256",
        },
        keyMaterial,
        DERIVED_BITS,
    );
    return Array.from(new Uint8Array(bits))
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
}
