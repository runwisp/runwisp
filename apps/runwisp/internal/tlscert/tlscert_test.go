// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tlscert

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureSelfSigned_GeneratesUsablePair(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := EnsureSelfSigned(dir, []string{"example.local", "10.0.0.5"})
	if err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}

	// Files land under <dataDir>/tls and load as a key pair.
	if filepath.Dir(certPath) != filepath.Join(dir, "tls") {
		t.Fatalf("cert path %q not under tls dir", certPath)
	}
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("generated pair does not load: %v", err)
	}

	// The key is written with restrictive permissions (it's a secret).
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("key perms = %o, want 600", perm)
	}
}

func TestEnsureSelfSigned_CoversRequestedHosts(t *testing.T) {
	dir := t.TempDir()
	certPath, _, err := EnsureSelfSigned(dir, DefaultHosts("0.0.0.0"))
	if err != nil {
		t.Fatalf("EnsureSelfSigned: %v", err)
	}
	leaf, err := loadLeaf(certPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		if err := leaf.VerifyHostname(host); err != nil {
			t.Errorf("cert should cover %q: %v", host, err)
		}
	}
}

func TestEnsureSelfSigned_ReusesExistingPair(t *testing.T) {
	dir := t.TempDir()
	hosts := []string{"example.local"}
	certPath, _, err := EnsureSelfSigned(dir, hosts)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}

	// A second call with the same hosts must reuse the on-disk cert untouched.
	certPath2, _, err := EnsureSelfSigned(dir, hosts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(certPath2)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("expected the existing cert to be reused, but it was regenerated")
	}
}

func TestEnsureSelfSigned_RegeneratesWhenHostAdded(t *testing.T) {
	dir := t.TempDir()
	certPath, _, err := EnsureSelfSigned(dir, []string{"example.local"})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(certPath)

	// Operator changed --host: the new address isn't in the existing SANs, so
	// the cert must be reissued to cover it.
	certPath2, _, err := EnsureSelfSigned(dir, []string{"example.local", "new.local"})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(certPath2)
	if string(first) == string(second) {
		t.Fatal("expected regeneration when a new host was requested")
	}
	leaf, _ := loadLeaf(certPath2)
	if err := leaf.VerifyHostname("new.local"); err != nil {
		t.Errorf("regenerated cert should cover new.local: %v", err)
	}
}

func TestUsableCert_RejectsExpired(t *testing.T) {
	dir := t.TempDir()
	hosts := []string{"example.local"}
	// Generate as if it were minted years ago, so it's already past expiry now.
	long := certValidity + renewBefore + 24*time.Hour
	if _, _, err := ensureSelfSigned(dir, hosts, time.Now().Add(-long)); err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, tlsDirName, certFileName)
	keyPath := filepath.Join(dir, tlsDirName, keyFileName)
	if usableCert(certPath, keyPath, hosts, time.Now()) {
		t.Fatal("an expired cert must not be considered usable")
	}
}

func TestFingerprint_StableAndMatchesDER(t *testing.T) {
	dir := t.TempDir()
	certPath, _, err := EnsureSelfSigned(dir, []string{"example.local"})
	if err != nil {
		t.Fatal(err)
	}
	fp1, err := Fingerprint(certPath)
	if err != nil {
		t.Fatal(err)
	}
	fp2, err := Fingerprint(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if fp1 != fp2 {
		t.Fatal("fingerprint must be stable for the same cert")
	}

	// File-based and DER-based fingerprints must agree — the CLI pins the DER
	// fingerprint off a live connection, the banner shows the file one.
	leaf, err := loadLeaf(certPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := FingerprintDER(leaf.Raw); got != fp1 {
		t.Fatalf("FingerprintDER = %q, Fingerprint = %q; must match", got, fp1)
	}
	if len(fp1) != 64 {
		t.Fatalf("expected 64 hex chars (sha256), got %d", len(fp1))
	}
}

func TestClassifyHosts_SplitsAndDedups(t *testing.T) {
	dns, ips := classifyHosts([]string{"a.local", "127.0.0.1", "a.local", "", "::1"})
	if len(dns) != 1 || dns[0] != "a.local" {
		t.Fatalf("dns names = %v, want [a.local]", dns)
	}
	if len(ips) != 2 {
		t.Fatalf("ip addrs = %v, want 2", ips)
	}
}
