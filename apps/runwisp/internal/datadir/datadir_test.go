// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package datadir

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestCleanPidFile_RemovesExisting(t *testing.T) {
	dataDir := t.TempDir()
	pid := PidFilePath(dataDir)
	if err := os.WriteFile(pid, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	CleanPidFile(dataDir)
	if _, err := os.Stat(pid); !os.IsNotExist(err) {
		t.Fatalf("expected PID file removed, got err=%v", err)
	}
}

// TestCleanPidFile_LeavesForeignPid guards H1: when daemon.pid names a process
// other than the caller (e.g. a second daemon clobbered it, or a stale file
// from an unrelated process), CleanPidFile must leave it in place so the live
// owner is not orphaned by another process's deferred cleanup.
func TestCleanPidFile_LeavesForeignPid(t *testing.T) {
	dataDir := t.TempDir()
	pid := PidFilePath(dataDir)
	foreign := os.Getpid() + 1
	if err := os.WriteFile(pid, []byte(strconv.Itoa(foreign)), 0600); err != nil {
		t.Fatal(err)
	}
	CleanPidFile(dataDir)
	if _, err := os.Stat(pid); err != nil {
		t.Fatalf("expected foreign PID file to be left in place, got err=%v", err)
	}
}

func TestCleanPidFile_MissingIsSilent(t *testing.T) {
	CleanPidFile(t.TempDir()) // must not panic, must not log fatal
}

// TestCleanPidFile_NonNotExistErrorLogsWarning covers the `err != nil &&
// !os.IsNotExist(err)` branch: when the PID-file path is in fact a populated
// directory, os.Remove returns ENOTEMPTY/EISDIR — CleanPidFile must swallow
// and log it without panicking.
func TestCleanPidFile_NonNotExistErrorLogsWarning(t *testing.T) {
	dataDir := t.TempDir()
	pidPath := PidFilePath(dataDir)
	if err := os.MkdirAll(pidPath, 0700); err != nil {
		t.Fatal(err)
	}
	// Put a file inside so os.Remove can't unlink the directory.
	if err := os.WriteFile(pidPath+"/keep", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	CleanPidFile(dataDir) // must not panic; logs a warning internally.
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("directory should still exist after non-removable Clean: %v", err)
	}
}

func TestWritePidFile_HappyPathThenReadBack(t *testing.T) {
	dataDir := t.TempDir()
	if err := WritePidFile(dataDir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(PidFilePath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("expected PID file mode 0600, got %#o", perm)
	}
	pid, err := ReadPidFile(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadPidFile = %d, want %d", pid, os.Getpid())
	}
}

func TestWritePidFile_OverwritesExistingRegularFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(PidFilePath(dataDir), []byte("999999"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := WritePidFile(dataDir); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPidFile(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadPidFile = %d, want %d", pid, os.Getpid())
	}
}

func TestWritePidFile_RefusesNonRegularPath(t *testing.T) {
	dataDir := t.TempDir()
	// Replace the future PID file path with a directory; WriteSecretFile must
	// reject "not a regular file" before any OpenFile attempt.
	if err := os.MkdirAll(PidFilePath(dataDir), 0700); err != nil {
		t.Fatal(err)
	}
	if err := WritePidFile(dataDir); err == nil {
		t.Fatal("expected WritePidFile to refuse non-regular path")
	}
}

func TestWriteSecretFile_Perms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := WriteSecretFile(path, []byte("shh")); err != nil {
		t.Fatalf("WriteSecretFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("perm = %o, want 0600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "shh" {
		t.Fatalf("content = %q, want %q", data, "shh")
	}
}

func TestWriteSecretFile_RefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecretFile(link, []byte("x")); err == nil {
		t.Fatal("expected WriteSecretFile to refuse a symlink path")
	}
}

func TestReadPidFile_TrimsAndParses(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(PidFilePath(dataDir), []byte("  4242\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pid, err := ReadPidFile(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 4242 {
		t.Fatalf("ReadPidFile = %d, want 4242", pid)
	}
}

func TestReadPidFile_MissingReturnsError(t *testing.T) {
	if _, err := ReadPidFile(t.TempDir()); err == nil {
		t.Fatal("expected error for missing PID file")
	}
}

func TestReadPidFile_GarbageContentsReturnsParseError(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(PidFilePath(dataDir), []byte("not-a-pid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPidFile(dataDir); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestPidFilePath_UnderDataDir(t *testing.T) {
	want := filepath.Join("/tmp/x", "daemon.pid")
	if got := PidFilePath("/tmp/x"); got != want {
		t.Fatalf("PidFilePath = %q, want %q", got, want)
	}
}

func TestRandBase62_LengthMatches(t *testing.T) {
	for _, n := range []int{1, 8, 32, 64} {
		s, err := RandBase62(n)
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != n {
			t.Fatalf("RandBase62(%d) = len %d, want %d", n, len(s), n)
		}
	}
}

func TestWritePidFile_AppendsNewline(t *testing.T) {
	dataDir := t.TempDir()
	if err := WritePidFile(dataDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(PidFilePath(dataDir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("PID file lacks trailing newline: %q", data)
	}
	if strings.TrimSpace(string(data)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("unexpected PID file body: %q", data)
	}
}

func TestSocketPath_UnderDataDir(t *testing.T) {
	dir := "/tmp/runwisp-test"
	want := filepath.Join(dir, "runwisp.sock")
	if got := SocketPath(dir); got != want {
		t.Fatalf("SocketPath(%q) = %q, want %q", dir, got, want)
	}
}
