// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package chap holds the daemon's challenge-response computation — the one
// formula every responder must agree on byte-for-byte: the Go server that
// verifies a login, the Go CLI that logs into a remote daemon, and the browser
// (which re-implements Response in WebCrypto, guarded by a shared test vector).
// Keeping the Go side in one pure function means server and CLI can never
// drift; the only hand-mirrored copy is the TypeScript one.
//
// The response is PBKDF2-HMAC-SHA256 over the password, salted with the
// single-use nonce. The slow KDF is what TLS-less / trusted-LAN operators rely
// on: a single SHA-256 made an intercepted transcript trivial to brute-force
// offline, whereas PBKDF2 with a high iteration count makes each password guess
// expensive. It is defense-in-depth behind TLS, not a replacement for it.
package chap

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/hex"
)

// Iterations is the PBKDF2 work factor. It MUST stay in sync with the browser
// (apps/ui/src/lib/api.ts) and is exercised by a cross-language test vector.
// 600k matches the OWASP guidance for PBKDF2-HMAC-SHA256 and stays well under a
// second in WebCrypto for an interactive login.
const Iterations = 600_000

// keyLength is the derived-key size in bytes; 32 = the full SHA-256 width.
const keyLength = 32

// Response computes the hex-encoded PBKDF2-HMAC-SHA256 of password salted with
// nonce. Both the server (to verify) and the CLI (to answer a challenge) call
// this, so they cannot disagree.
func Response(password, nonce string) string {
	key, err := pbkdf2.Key(sha256.New, password, []byte(nonce), Iterations, keyLength)
	if err != nil {
		// pbkdf2.Key only errors on absurd parameters (keyLength overflow); our
		// arguments are compile-time constants, so this is unreachable.
		panic("chap: pbkdf2 derivation failed: " + err.Error())
	}
	return hex.EncodeToString(key)
}
