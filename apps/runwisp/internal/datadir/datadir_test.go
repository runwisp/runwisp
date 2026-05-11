// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// fakePasswordStore is an in-memory PasswordStore for tests.
type fakePasswordStore struct {
	values map[string]string
}

func newFakeStore() *fakePasswordStore {
	return &fakePasswordStore{values: map[string]string{}}
}

func (f *fakePasswordStore) GetConfigValue(key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

func (f *fakePasswordStore) SetConfigValue(key, value string) error {
	f.values[key] = value
	return nil
}

// TestResolvePassword_EnvVarNotPersisted guards the prime-directive contract:
// when an operator sets RUNWISP_PASSWORD (Docker secret, systemd
// LoadCredential, sealed-secrets), the daemon must use the value in memory
// only. Writing it to SQLite defeats the operator's whole reason for using a
// secret manager.
func TestResolvePassword_EnvVarNotPersisted(t *testing.T) {
	store := newFakeStore()
	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	got, isNew, err := ResolvePassword(store)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env-secret" {
		t.Fatalf("expected env value, got %q", got)
	}
	if isNew {
		t.Fatal("env-supplied password must not be reported as newly generated")
	}
	if _, ok := store.values[passwordKey]; ok {
		t.Fatal("env-supplied password must not be written to the store")
	}
}

// TestResolvePassword_EnvVarDoesNotOverwriteStoredRow verifies that an
// existing stored password row is left untouched when RUNWISP_PASSWORD is set.
func TestResolvePassword_EnvVarDoesNotOverwriteStoredRow(t *testing.T) {
	store := newFakeStore()
	if err := store.SetConfigValue(passwordKey, "old-stored-secret"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	got, _, err := ResolvePassword(store)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env-secret" {
		t.Fatalf("env must take precedence in-memory, got %q", got)
	}
	if store.values[passwordKey] != "old-stored-secret" {
		t.Fatalf("stored password must not be rewritten when env var is set; got %q", store.values[passwordKey])
	}
}

// TestResolvePassword_StoreFallbackUnchanged verifies the unchanged path:
// no env var, stored row present → use it.
func TestResolvePassword_StoreFallbackUnchanged(t *testing.T) {
	store := newFakeStore()
	if err := store.SetConfigValue(passwordKey, "from-store"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNWISP_PASSWORD", "")

	got, isNew, err := ResolvePassword(store)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-store" || isNew {
		t.Fatalf("expected stored password; got=%q isNew=%v", got, isNew)
	}
}

// TestResolvePassword_GeneratesAndPersistsWhenEmpty verifies that absent
// both env var and stored row, a new password is generated AND written.
func TestResolvePassword_GeneratesAndPersistsWhenEmpty(t *testing.T) {
	store := newFakeStore()
	t.Setenv("RUNWISP_PASSWORD", "")

	got, isNew, err := ResolvePassword(store)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew || got == "" {
		t.Fatalf("expected freshly generated password; got=%q isNew=%v", got, isNew)
	}
	if store.values[passwordKey] != got {
		t.Fatalf("stored value does not match returned password: store=%q returned=%q", store.values[passwordKey], got)
	}
}
