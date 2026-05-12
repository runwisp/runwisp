// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestSRPDriftVector locks the Go-side SRP verifier to a fixed
// (identity, password, salt, iteration count) tuple shared with the
// browser-side test in apps/ui/src/lib/srp-vector.test.ts. Any silent drift
// in the KDF, the group, or the identity binding breaks login on one side
// but not the other; this test (paired with its TS counterpart) catches it
// before the operator does.
func TestSRPDriftVector(t *testing.T) {
	type vector struct {
		Identity            string `json:"identity"`
		Password            string `json:"password"`
		SaltHex             string `json:"salt_hex"`
		Iterations          int    `json:"iterations"`
		ExpectedVerifierHex string `json:"expected_verifier_hex"`
	}

	path := resolveSRPVectorPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read vector %s: %v", path, err)
	}
	var v vector
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse vector: %v", err)
	}

	if v.Identity != SRPIdentity {
		t.Fatalf("vector identity %q does not match daemon constant %q", v.Identity, SRPIdentity)
	}
	if v.Iterations != pbkdf2Iterations {
		t.Fatalf("vector iterations %d does not match daemon constant %d", v.Iterations, pbkdf2Iterations)
	}

	salt, err := hex.DecodeString(v.SaltHex)
	if err != nil {
		t.Fatalf("decode salt hex: %v", err)
	}
	got, err := DeriveSRPVerifier(v.Password, salt)
	if err != nil {
		t.Fatalf("derive verifier: %v", err)
	}
	if hex.EncodeToString(got) != v.ExpectedVerifierHex {
		t.Fatalf("verifier drift:\n  expected %s\n       got %s",
			v.ExpectedVerifierHex, hex.EncodeToString(got))
	}
}

// resolveSRPVectorPath walks up from the test's working directory until it
// finds packages/common/src/srp-test-vector.json. We avoid hard-coding a
// repo-root path so the test stays portable across worktrees and CI layouts.
func resolveSRPVectorPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	rel := filepath.Join("packages", "common", "src", "srp-test-vector.json")
	for {
		candidate := filepath.Join(dir, rel)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate %s starting from test working dir", rel)
		}
		dir = parent
	}
}
