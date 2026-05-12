// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	srp "mz.attahri.com/code/srp/v3"
)

func TestGenerateJWTSecret_Unique(t *testing.T) {
	a, err := GenerateJWTSecret()
	if err != nil {
		t.Fatal(err)
	}

	if len(a) < 20 {
		t.Fatalf("expected JWT secret of reasonable length, got %d chars", len(a))
	}

	b, err := GenerateJWTSecret()
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two generated secrets should not be identical")
	}
}

func TestGeneratePassword_Base62Alphabet(t *testing.T) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

	password, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}

	if len(password) != 22 {
		t.Fatalf("expected password length 22, got %d", len(password))
	}

	for _, char := range password {
		if !strings.ContainsRune(alphabet, char) {
			t.Fatalf("password contains non-base62 character %q", char)
		}
	}
}

func TestGeneratePassword_Unique(t *testing.T) {
	first, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GeneratePassword()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two generated passwords should not be identical")
	}
}

func TestEnsureDir_Mode0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("expected data dir mode 0700, got %#o", perm)
	}
}

func TestWritePidFile_RefusesSymlink(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, PidFilePath(dataDir)); err != nil {
		t.Fatal(err)
	}

	if err := WritePidFile(dataDir); err == nil {
		t.Fatal("expected WritePidFile to refuse a symlinked path")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "untouched" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}

// fakeSecretStore is an in-memory SecretStore for tests.
type fakeSecretStore struct {
	values map[string]string
}

func newFakeStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}}
}

func (f *fakeSecretStore) GetSecret(key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeSecretStore) SetSecret(key, value string) error {
	f.values[key] = value
	return nil
}

func (f *fakeSecretStore) DeleteConfigValue(key string) error {
	delete(f.values, key)
	return nil
}

// TestResolveSRPCredentials_EnvVarNotPersisted guards the prime-directive
// contract: when an operator sets RUNWISP_PASSWORD (Docker secret, systemd
// LoadCredential, sealed-secrets), the daemon must use the value in memory
// only. Writing it to SQLite defeats the operator's whole reason for using a
// secret manager.
func TestResolveSRPCredentials_EnvVarNotPersisted(t *testing.T) {
	store := newFakeStore()
	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	creds, generated, err := ResolveSRPCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(creds.Verifier) == 0 || len(creds.Salt) == 0 {
		t.Fatal("env-supplied password must still produce SRP credentials in memory")
	}
	if generated != "" {
		t.Fatal("env-supplied password must not be reported as generated")
	}
	if _, ok := store.values[srpVerifierKey]; ok {
		t.Fatal("env-supplied verifier must not be persisted")
	}
	if _, ok := store.values[srpSaltKey]; ok {
		t.Fatal("env-supplied salt must not be persisted")
	}
}

// TestResolveSRPCredentials_GeneratesAndPersistsWhenEmpty verifies that
// absent both env var and stored rows, fresh credentials are generated AND
// the password is returned for one-shot disclosure.
func TestResolveSRPCredentials_GeneratesAndPersistsWhenEmpty(t *testing.T) {
	store := newFakeStore()
	t.Setenv("RUNWISP_PASSWORD", "")

	creds, generated, err := ResolveSRPCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if generated == "" {
		t.Fatal("expected generated password to be returned on first boot")
	}
	if len(creds.Verifier) == 0 || len(creds.Salt) == 0 {
		t.Fatal("expected verifier and salt to be generated")
	}
	if store.values[srpVerifierKey] != hex.EncodeToString(creds.Verifier) {
		t.Fatal("verifier was not persisted in the store")
	}
	if store.values[srpSaltKey] != hex.EncodeToString(creds.Salt) {
		t.Fatal("salt was not persisted in the store")
	}
}

// TestResolveSRPCredentials_ReusesStored verifies that an existing stored
// pair is read back verbatim and no fresh password is generated.
func TestResolveSRPCredentials_ReusesStored(t *testing.T) {
	store := newFakeStore()
	t.Setenv("RUNWISP_PASSWORD", "")

	// Seed via a first call.
	first, generated, err := ResolveSRPCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if generated == "" {
		t.Fatal("expected first call to generate")
	}

	// Second call should be a no-op load.
	second, generated, err := ResolveSRPCredentials(store)
	if err != nil {
		t.Fatal(err)
	}
	if generated != "" {
		t.Fatal("second call must not generate a fresh password")
	}
	if hex.EncodeToString(first.Verifier) != hex.EncodeToString(second.Verifier) {
		t.Fatal("verifier was not preserved")
	}
	if hex.EncodeToString(first.Salt) != hex.EncodeToString(second.Salt) {
		t.Fatal("salt was not preserved")
	}
}

// TestResolveSRPCredentials_RefusesLegacyPasswordRow guarantees no silent
// migration from a plaintext password row to SRP credentials.
func TestResolveSRPCredentials_RefusesLegacyPasswordRow(t *testing.T) {
	store := newFakeStore()
	if err := store.SetSecret(passwordKey, "legacy-secret"); err != nil {
		t.Fatal(err)
	}

	_, _, err := ResolveSRPCredentials(store)
	if err == nil {
		t.Fatal("expected refusal when legacy password row present")
	}
	if !strings.Contains(err.Error(), "legacy plaintext password row") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDeriveSRPVerifier_RoundTrip exercises the verifier/salt pair against
// the SRP library's own client to confirm interoperability with our params.
func TestDeriveSRPVerifier_RoundTrip(t *testing.T) {
	salt, err := GenerateSRPSalt()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := DeriveSRPVerifier("p@ssw0rd!", salt)
	if err != nil {
		t.Fatal(err)
	}
	server, err := srp.NewServer(SRPParams(), SRPIdentity, salt, verifier)
	if err != nil {
		t.Fatal(err)
	}
	client, err := srp.NewClient(SRPParams(), SRPIdentity, "p@ssw0rd!", salt)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetB(server.B()); err != nil {
		t.Fatal(err)
	}
	if err := server.SetA(client.A()); err != nil {
		t.Fatal(err)
	}
	M1, err := client.ComputeM1()
	if err != nil {
		t.Fatal(err)
	}
	ok, err := server.CheckM1(M1)
	if err != nil || !ok {
		t.Fatalf("server failed to verify client proof: ok=%v err=%v", ok, err)
	}
	M2, err := server.ComputeM2()
	if err != nil {
		t.Fatal(err)
	}
	ok, err = client.CheckM2(M2)
	if err != nil || !ok {
		t.Fatal("client failed to verify server proof")
	}
}

// TestDeriveSRPVerifier_WrongPasswordFails confirms a different password
// produces a non-matching verifier and the handshake fails.
func TestDeriveSRPVerifier_WrongPasswordFails(t *testing.T) {
	salt, err := GenerateSRPSalt()
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := DeriveSRPVerifier("right-password", salt)
	if err != nil {
		t.Fatal(err)
	}
	server, err := srp.NewServer(SRPParams(), SRPIdentity, salt, verifier)
	if err != nil {
		t.Fatal(err)
	}
	client, err := srp.NewClient(SRPParams(), SRPIdentity, "wrong-password", salt)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetB(server.B()); err != nil {
		t.Fatal(err)
	}
	if err := server.SetA(client.A()); err != nil {
		t.Fatal(err)
	}
	M1, err := client.ComputeM1()
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := server.CheckM1(M1)
	if ok {
		t.Fatal("server accepted wrong password — verifier comparison is broken")
	}
}

// silence "errors" unused if Compile drops branches.
var _ = errors.New
