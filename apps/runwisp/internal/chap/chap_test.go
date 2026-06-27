// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package chap

import "testing"

// These vectors are the cross-language contract. The identical (password,
// nonce) → response pairs are asserted in the browser at
// apps/ui/src/lib/api.test.ts; if either side changes the KDF formula,
// iteration count, salt, or output length, one of the two test suites breaks
// and forces them back in sync.
var responseVectors = []struct {
	name     string
	password string
	nonce    string
	want     string
}{
	{"doc example", "password", "nonce", "736b9e46edb32dbde0382c376f35dd8cf79b8338cff806a9cc2fccf588b08c44"},
	{"hex nonce", "hunter2", "0123456789abcdef", "52e61d8f37e5294458ac2225126008ca1e32c7847b228221d574dc305f40f8ce"},
}

func TestResponse_KnownVectors(t *testing.T) {
	for _, v := range responseVectors {
		t.Run(v.name, func(t *testing.T) {
			if got := Response(v.password, v.nonce); got != v.want {
				t.Fatalf("Response(%q, %q) = %q, want %q", v.password, v.nonce, got, v.want)
			}
		})
	}
}

func TestResponse_DiffersByNonce(t *testing.T) {
	a := Response("pw", "nonce-a")
	b := Response("pw", "nonce-b")
	if a == b {
		t.Fatal("response must depend on the nonce; got identical output for different nonces")
	}
}

func TestResponse_DiffersByPassword(t *testing.T) {
	a := Response("pw-a", "nonce")
	b := Response("pw-b", "nonce")
	if a == b {
		t.Fatal("response must depend on the password; got identical output for different passwords")
	}
}

// TestIterations_PinnedValue makes a change to the work factor loud: the value
// is half the cross-language contract, so bumping it must be a deliberate,
// coordinated edit (Go + browser) rather than an accidental tweak.
func TestIterations_PinnedValue(t *testing.T) {
	if Iterations != 600_000 {
		t.Fatalf("Iterations = %d; changing it requires updating apps/ui/src/lib/api.ts and the test vectors", Iterations)
	}
}
