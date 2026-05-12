// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/storage"
)

// fakeSecretStore is an in-memory SecretStore for daemon_config tests.
type fakeSecretStore struct {
	values map[string]string
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[string]string{}}
}

func (f *fakeSecretStore) GetConfigValue(key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakeSecretStore) SetConfigValue(key, value string) error {
	f.values[key] = value
	return nil
}

func (f *fakeSecretStore) DeleteConfigValue(key string) error {
	delete(f.values, key)
	return nil
}

func (f *fakeSecretStore) GetSecret(key string) (string, bool, error) {
	return f.GetConfigValue(key)
}

func (f *fakeSecretStore) SetSecret(key, value string) error {
	return f.SetConfigValue(key, value)
}

// TestHashEnvPassword_NotPlainSHA256 confirms the stored fingerprint is
// keyed HMAC, not a bare sha256 of the password — a leaked row must not
// give an offline attacker enough to dictionary-attack the password.
func TestHashEnvPassword_NotPlainSHA256(t *testing.T) {
	got := hashEnvPassword("jwt-secret-A", "password-A")

	plain := sha256.Sum256([]byte("password-A"))
	if got == hex.EncodeToString(plain[:]) {
		t.Fatalf("hashEnvPassword still produces a plain SHA-256 fingerprint")
	}

	expected := hmac.New(sha256.New, []byte("jwt-secret-A"))
	expected.Write([]byte("password-A"))
	if got != hex.EncodeToString(expected.Sum(nil)) {
		t.Fatalf("hashEnvPassword does not match HMAC-SHA256(jwt-secret, password)")
	}
}

// TestHashEnvPassword_KeyedByJWTSecret confirms a different JWT secret
// changes the fingerprint even when the password is identical. Two daemons
// with the same RUNWISP_PASSWORD on different installations must produce
// different fingerprints.
func TestHashEnvPassword_KeyedByJWTSecret(t *testing.T) {
	a := hashEnvPassword("jwt-secret-A", "password-A")
	b := hashEnvPassword("jwt-secret-B", "password-A")
	if a == b {
		t.Fatalf("identical fingerprint for two daemons sharing a password but not a JWT secret")
	}
}

// TestResolveJWTSecret_FreshBoot generates a JWT secret and records the
// env-password fingerprint on the very first call.
func TestResolveJWTSecret_FreshBoot(t *testing.T) {
	store := newFakeSecretStore()
	secret, err := resolveJWTSecret(store, "fresh-password")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Fatal("expected a non-empty JWT secret")
	}

	storedSecret, ok, err := store.GetSecret(storage.ConfigKeyJWTSecret)
	if err != nil || !ok || storedSecret != secret {
		t.Fatalf("expected JWT secret persisted, got ok=%v stored=%q secret=%q err=%v", ok, storedSecret, secret, err)
	}

	storedHash, ok, err := store.GetSecret(storage.ConfigKeyEnvPasswordSum)
	if err != nil || !ok {
		t.Fatalf("expected env_password_hash row to exist after first boot")
	}
	if storedHash != hashEnvPassword(secret, "fresh-password") {
		t.Fatalf("stored hash %q is not HMAC under the new JWT secret", storedHash)
	}
}

// TestResolveJWTSecret_UnchangedPasswordKeepsSecret confirms two consecutive
// boots with the same RUNWISP_PASSWORD do not rotate the JWT secret.
func TestResolveJWTSecret_UnchangedPasswordKeepsSecret(t *testing.T) {
	store := newFakeSecretStore()
	first, err := resolveJWTSecret(store, "same-password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveJWTSecret(store, "same-password")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("JWT secret rotated despite unchanged password: %q → %q", first, second)
	}
}

// TestResolveJWTSecret_ChangedPasswordRotatesSecret confirms a different
// RUNWISP_PASSWORD on the second boot rotates the JWT and rewrites the
// stored env-password fingerprint under the new secret.
func TestResolveJWTSecret_ChangedPasswordRotatesSecret(t *testing.T) {
	store := newFakeSecretStore()
	first, err := resolveJWTSecret(store, "password-A")
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolveJWTSecret(store, "password-B")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("JWT secret did not rotate after password change")
	}

	storedHash, _, err := store.GetSecret(storage.ConfigKeyEnvPasswordSum)
	if err != nil {
		t.Fatal(err)
	}
	if storedHash != hashEnvPassword(second, "password-B") {
		t.Fatal("env-password fingerprint not rewritten under the new JWT secret")
	}
}

// TestResolveJWTSecret_LegacySHA256TriggersOneRotation simulates an upgrade
// from the old SHA-256 fingerprint scheme: a stored hash that doesn't match
// the HMAC of the current password rotates exactly once. The next boot with
// the same password must be stable.
func TestResolveJWTSecret_LegacySHA256TriggersOneRotation(t *testing.T) {
	store := newFakeSecretStore()

	// Seed: pre-existing JWT secret + pre-existing SHA-256 hash of "p".
	if err := store.SetSecret(storage.ConfigKeyJWTSecret, "pre-upgrade-secret"); err != nil {
		t.Fatal(err)
	}
	legacy := sha256.Sum256([]byte("p"))
	if err := store.SetSecret(storage.ConfigKeyEnvPasswordSum, hex.EncodeToString(legacy[:])); err != nil {
		t.Fatal(err)
	}

	first, err := resolveJWTSecret(store, "p")
	if err != nil {
		t.Fatal(err)
	}
	if first == "pre-upgrade-secret" {
		t.Fatal("expected JWT secret to rotate when migrating from legacy SHA-256 fingerprint")
	}

	second, err := resolveJWTSecret(store, "p")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected migration to converge after one rotation: %q → %q", first, second)
	}
}

// TestHashEnvPassword_IsHex confirms the fingerprint is a hex-encoded
// SHA-256 (64 chars). Other formats break the change-detection comparison
// without a clear failure mode.
func TestHashEnvPassword_IsHex(t *testing.T) {
	got := hashEnvPassword("k", "p")
	if len(got) != 64 {
		t.Fatalf("expected 64 hex chars, got %d (%q)", len(got), got)
	}
	if strings.IndexFunc(got, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'))
	}) >= 0 {
		t.Fatalf("non-hex character in fingerprint %q", got)
	}
}
