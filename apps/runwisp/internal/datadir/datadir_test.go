// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	dir := filepath.Join(t.TempDir(), ".runwisp")
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

func TestSocketPath_UnderDataDir(t *testing.T) {
	dir := "/tmp/runwisp-test"
	want := filepath.Join(dir, "runwisp.sock")
	if got := SocketPath(dir); got != want {
		t.Fatalf("SocketPath(%q) = %q, want %q", dir, got, want)
	}
}
