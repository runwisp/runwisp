// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package datadir

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"log/slog"
)

// EnsureDir creates dir (and parents) with mode 0700 so that secrets stored
// inside are not exposed to other local users via directory traversal.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// MkdirAll leaves a pre-existing directory's mode untouched, so a data dir
	// created out-of-band (e.g. a Docker bind-mount at 0755) would silently keep
	// looser perms and expose the SQLite DB and other non-secret-file artifacts.
	// Enforce 0700 unconditionally to hold the guarantee this function promises.
	return os.Chmod(dir, 0700)
}

// WriteSecretFile writes data to path with mode 0600, refusing to follow
// symlinks. If path already exists, it must be a regular file owned by the
// caller; otherwise the write is rejected. This prevents a TOCTOU symlink
// attack where another local user replaces a file with a symlink to a
// sensitive target the caller can write (e.g. ~/.ssh/authorized_keys). It is
// the shared primitive for any secret-bearing file (PID file, daemon secrets,
// the CLI's cached JWT); callers must EnsureDir the parent first.
func WriteSecretFile(path string, data []byte) error {
	if err := checkSecretFileTarget(path); err != nil {
		return err
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC | syscall.O_NOFOLLOW
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// checkSecretFileTarget refuses a path that isn't safe to open for a secret
// write: a symlink (TOCTOU risk), a non-regular file, or an existing regular
// file owned by a different user. Shared by WriteSecretFile and
// AcquireDaemonLock, which both open PidFilePath under the same threat model.
func checkSecretFileTarget(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write %s: path is a symlink", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to write %s: not a regular file", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("refusing to write %s: owned by a different user", path)
	}
	return nil
}

// RandBase62 returns a cryptographically random base62 string of n characters.
// Each character contributes ~5.954 bits of entropy.
func RandBase62(n int) (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("failed to generate random base62 string: %w", err)
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b), nil
}

// GeneratePassword returns a cryptographically random base62 password (128+ bits of entropy).
func GeneratePassword() (string, error) {
	return RandBase62(22)
}

// PidFilePath returns the path to the daemon PID file.
func PidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.pid")
}

func WritePidFile(dataDir string) error {
	return WriteSecretFile(PidFilePath(dataDir), []byte(strconv.Itoa(os.Getpid())+"\n"))
}

func ReadPidFile(dataDir string) (int, error) {
	data, err := os.ReadFile(PidFilePath(dataDir))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// CleanPidFile removes the PID file, but only when it still holds this
// process's own PID. A second daemon that clobbered the file (or a stale file
// belonging to an unrelated process) is left untouched so a live daemon is
// never orphaned by another process's cleanup.
func CleanPidFile(dataDir string) {
	path := PidFilePath(dataDir)
	pid, err := ReadPidFile(dataDir)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("Failed to read PID file before cleanup", "err", err)
		}
		return
	}
	if pid != os.Getpid() {
		// The file names a different process — not ours to remove.
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove PID file", "err", err)
	}
}

// DaemonLock is the held ownership claim returned by AcquireDaemonLock. The
// zero value is not usable; obtain one only via AcquireDaemonLock.
type DaemonLock struct {
	f *os.File
}

// AcquireDaemonLock atomically claims ownership of dataDir's PID file using an
// OS-level advisory lock (flock), so two `runwisp daemon` processes racing on
// the same data dir can never both proceed past this call. Unlike a
// read-then-write PID file, the lock is held for the caller's entire lifetime
// and is automatically released by the kernel if the process dies (including
// SIGKILL) — so a crashed daemon's "ownership" disappears the instant the
// process exits, with no separate stale-PID heuristic needed.
func AcquireDaemonLock(dataDir string) (*DaemonLock, error) {
	path := PidFilePath(dataDir)
	if err := checkSecretFileTarget(path); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		existingPid := "unknown"
		if pid, readErr := ReadPidFile(dataDir); readErr == nil {
			existingPid = strconv.Itoa(pid)
		}
		_ = f.Close()
		return nil, fmt.Errorf("a RunWisp daemon is already running for data dir %q (pid %s); stop it first", dataDir, existingPid)
	}

	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}
	if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, err
	}

	return &DaemonLock{f: f}, nil
}

// Release unlocks and removes the PID file, then closes the fd. Safe to
// always remove: holding the lock while removing means no other process can
// be treating this file as live in the meantime. Best-effort, mirroring
// CleanPidFile: unexpected failures are logged via slog.Warn, never panicked.
func (l *DaemonLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		slog.Warn("Failed to release daemon lock", "err", err)
	}
	if err := os.Remove(l.f.Name()); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove PID file", "err", err)
	}
	if err := l.f.Close(); err != nil {
		slog.Warn("Failed to close daemon lock file", "err", err)
	}
}

// SocketPath returns the path to the daemon's Unix domain socket. The socket
// lives inside the (0700) data dir, so its existence and reachability are
// gated by filesystem permissions on the directory; the daemon additionally
// chmod's the socket itself to 0600 and verifies peer UID at accept time.
func SocketPath(dataDir string) string {
	return filepath.Join(dataDir, "runwisp.sock")
}
