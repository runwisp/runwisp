// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package datadir

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureDir_TightensPreExistingPerms guards the regression where EnsureDir
// relied solely on os.MkdirAll, which never chmods an already-existing directory
// — so a data dir created out-of-band at 0755 (e.g. a Docker bind-mount) kept
// looser perms and exposed the SQLite DB to other local users.
func TestEnsureDir_TightensPreExistingPerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0700 {
		t.Fatalf("expected 0700 on pre-existing dir, got %o", perm)
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

func TestSocketPath_UnderDataDir(t *testing.T) {
	dir := "/tmp/runwisp-test"
	want := filepath.Join(dir, "runwisp.sock")
	if got := SocketPath(dir); got != want {
		t.Fatalf("SocketPath(%q) = %q, want %q", dir, got, want)
	}
}

// TestAcquireDaemonLock_SucceedsWhenFree guards the happy path: nothing else
// holds the PID file, so the lock is granted and the PID file is populated.
func TestAcquireDaemonLock_SucceedsWhenFree(t *testing.T) {
	dataDir := t.TempDir()
	lock, err := AcquireDaemonLock(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	pid, err := ReadPidFile(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Fatalf("ReadPidFile = %d, want %d", pid, os.Getpid())
	}
}

// TestAcquireDaemonLock_SecondCallConflicts is the regression test for the
// TOCTOU race: a second daemon racing on the same data dir must be refused
// while the first lock is held, not merely warned. flock is scoped per open
// file description, so a second *open* of the same path from this same
// process still conflicts at the OS level — a valid stand-in for "two
// processes."
func TestAcquireDaemonLock_SecondCallConflicts(t *testing.T) {
	dataDir := t.TempDir()
	first, err := AcquireDaemonLock(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	if _, err := AcquireDaemonLock(dataDir); err == nil {
		t.Fatal("expected second AcquireDaemonLock to fail while the first lock is held")
	}
}

// TestAcquireDaemonLock_ReacquireAfterRelease confirms Release genuinely frees
// the lock (and removes the PID file) rather than leaving it in a state that
// permanently wedges the data dir.
func TestAcquireDaemonLock_ReacquireAfterRelease(t *testing.T) {
	dataDir := t.TempDir()
	first, err := AcquireDaemonLock(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()

	if _, err := os.Stat(PidFilePath(dataDir)); !os.IsNotExist(err) {
		t.Fatalf("expected PID file removed after Release, got err=%v", err)
	}

	second, err := AcquireDaemonLock(dataDir)
	if err != nil {
		t.Fatalf("expected re-acquire after Release to succeed: %v", err)
	}
	defer second.Release()
}

// TestWriteSecretFile_AtomicNoTempFileLeftBehind guards the crash-safety fix:
// WriteSecretFile must go through a temp-file-in-same-dir + rename, and must
// never leave a stray .tmp-* file behind in the happy path (which would
// otherwise accumulate in the data dir forever).
func TestWriteSecretFile_AtomicNoTempFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := WriteSecretFile(path, []byte("v1")); err != nil {
		t.Fatalf("WriteSecretFile: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "secret" {
		t.Fatalf("expected only the final file in dir, got %v", entries)
	}
}

// TestWriteSecretFile_FailedWriteLeavesOriginalIntact is the regression test
// for the non-atomic O_TRUNC write: a failure partway through must never
// leave a truncated or partially-written file at path. Simulated by making
// the directory unwritable after the original file exists, so the temp file
// can't be created — the pre-existing content at path must survive untouched.
func TestWriteSecretFile_FailedWriteLeavesOriginalIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) }) // let t.TempDir() clean up

	if err := WriteSecretFile(path, []byte("new-and-longer-content")); err == nil {
		t.Fatal("expected WriteSecretFile to fail when the temp file can't be created")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "original" {
		t.Fatalf("original content was clobbered by a failed write: %q", got)
	}
}

// TestWriteSecretFile_TempFileNotRenamedOnWriteError is the direct regression
// test for "a failed write must never leave a truncated file at the target
// path": when the temp file can't be created at all (parent dir missing),
// WriteSecretFile must fail without ever touching path, and must not leave a
// temp file behind anywhere.
func TestWriteSecretFile_TempFileNotRenamedOnWriteError(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "missing-parent", "secret")

	// Seed nothing at path (parent doesn't even exist) — WriteSecretFile must
	// fail cleanly, per its documented contract that callers EnsureDir first.
	if err := WriteSecretFile(path, []byte("new")); err == nil {
		t.Fatal("expected WriteSecretFile to fail when the parent dir doesn't exist")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file at path after a failed write, got err=%v", err)
	}
}

// TestWriteSecretFile_RefusesForeignOwner guards the ownership check the
// WriteSecretFile doc comment has always claimed but the code never enforced
// until now. Only root can chown a file away from the current euid, so an
// unprivileged test sandbox skips rather than faking a UID mismatch in a way
// that would weaken the real check.
func TestWriteSecretFile_RefusesForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to create a file owned by a different UID")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	const foreignUID = 1 // "daemon" on most Linux distros; distinct from root (0)
	if err := os.Chown(path, foreignUID, -1); err != nil {
		t.Fatal(err)
	}
	if err := WriteSecretFile(path, []byte("y")); err == nil {
		t.Fatal("expected WriteSecretFile to refuse a file owned by a different user")
	}
}
