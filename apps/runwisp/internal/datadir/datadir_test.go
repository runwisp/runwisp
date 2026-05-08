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

// TestResolvePassword_EnvVarNotPersisted guards the prime-directive contract
// for #2: when an operator sets RUNWISP_PASSWORD (Docker secret, systemd
// LoadCredential, sealed-secrets), the daemon must use the value in memory
// only. Writing it to data/password defeats the operator's whole reason for
// using a secret manager.
func TestResolvePassword_EnvVarNotPersisted(t *testing.T) {
	dataDir := t.TempDir()
	pwPath := filepath.Join(dataDir, "password")

	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	got, isNew, err := ResolvePassword(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env-secret" {
		t.Fatalf("expected env value, got %q", got)
	}
	if isNew {
		t.Fatal("env-supplied password must not be reported as newly generated")
	}
	if _, err := os.Stat(pwPath); !os.IsNotExist(err) {
		t.Fatalf("data/password must not be created when RUNWISP_PASSWORD is set; stat err=%v", err)
	}
}

// TestResolvePassword_EnvVarDoesNotOverwriteExistingFile verifies that an
// existing data/password file is left untouched when RUNWISP_PASSWORD is
// set. (Previously the daemon would overwrite the file with the env value
// on every start.)
func TestResolvePassword_EnvVarDoesNotOverwriteExistingFile(t *testing.T) {
	dataDir := t.TempDir()
	pwPath := filepath.Join(dataDir, "password")
	if err := os.WriteFile(pwPath, []byte("old-file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	got, _, err := ResolvePassword(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env-secret" {
		t.Fatalf("env must take precedence in-memory, got %q", got)
	}

	onDisk, err := os.ReadFile(pwPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(onDisk)) != "old-file-secret" {
		t.Fatalf("data/password must not be rewritten when env var is set; got %q", strings.TrimSpace(string(onDisk)))
	}
}

// TestResolvePassword_FileFallbackUnchanged verifies the unchanged path: no
// env var, file present → use file.
func TestResolvePassword_FileFallbackUnchanged(t *testing.T) {
	dataDir := t.TempDir()
	pwPath := filepath.Join(dataDir, "password")
	if err := os.WriteFile(pwPath, []byte("from-file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RUNWISP_PASSWORD", "")

	got, isNew, err := ResolvePassword(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-file" || isNew {
		t.Fatalf("expected file-backed password; got=%q isNew=%v", got, isNew)
	}
}

// TestResolvePassword_GeneratesAndPersistsWhenEmpty verifies that absent
// both env var and file, a new password is generated AND written.
func TestResolvePassword_GeneratesAndPersistsWhenEmpty(t *testing.T) {
	dataDir := t.TempDir()
	pwPath := filepath.Join(dataDir, "password")
	t.Setenv("RUNWISP_PASSWORD", "")

	got, isNew, err := ResolvePassword(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !isNew || got == "" {
		t.Fatalf("expected freshly generated password; got=%q isNew=%v", got, isNew)
	}
	onDisk, err := os.ReadFile(pwPath)
	if err != nil {
		t.Fatalf("expected data/password to be written when generating: %v", err)
	}
	if strings.TrimSpace(string(onDisk)) != got {
		t.Fatalf("file content does not match returned password")
	}
}

func TestWriteSecretFile_RefusesSymlink(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	pwPath := filepath.Join(dataDir, "password")
	if err := os.Symlink(target, pwPath); err != nil {
		t.Fatal(err)
	}

	if err := writeSecretFile(pwPath, "newpw"); err == nil {
		t.Fatal("expected writeSecretFile to refuse a symlinked path")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "untouched" {
		t.Fatalf("symlink target was modified: %q", got)
	}
}
