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
	"time"

	"log/slog"
)

// EnsureDir creates dir (and parents) with mode 0700 so that secrets stored
// inside are not exposed to other local users via directory traversal.
func EnsureDir(dir string) error {
	return ensureDir(dir, os.Chmod)
}

// ensureDir is EnsureDir with the chmod injectable, so the not-the-owner case
// can be tested without a second user.
func ensureDir(dir string, chmod func(string, os.FileMode) error) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// MkdirAll leaves a pre-existing directory's mode untouched, so a data dir
	// created out-of-band (e.g. a Docker bind-mount at 0755) would silently keep
	// looser perms and expose the SQLite DB and other non-secret-file artifacts.
	// Tighten it when it differs.
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() == 0700 {
		return nil
	}
	if err := chmod(dir, 0700); err != nil {
		// A writable dir the process doesn't own (a Kubernetes fsGroup volume, a
		// group-writable bind mount) can't be chmod'ed. Refusing to start would make
		// every command fail; the secrets inside are 0600 regardless
		// (WriteSecretFile), so warn instead.
		if errors.Is(err, os.ErrPermission) {
			slog.Warn("Data dir permissions are looser than 0700 and can't be tightened", "dir", dir, "mode", info.Mode().Perm(), "err", err)
			return nil
		}
		return err
	}
	return nil
}

// WriteSecretFile writes data to path with mode 0600, refusing to follow
// symlinks. If path already exists, it must be a regular file owned by the
// caller; otherwise the write is rejected. This prevents a TOCTOU symlink
// attack where another local user replaces a file with a symlink to a
// sensitive target the caller can write (e.g. ~/.ssh/authorized_keys). It is
// the shared primitive for any secret-bearing file (PID file, daemon secrets,
// the CLI's cached JWT); callers must EnsureDir the parent first.
//
// The write is atomic: data lands in a temp file in the same directory (so
// the final rename is same-filesystem), is fsync'd, and only then replaces
// path via os.Rename. A crash (SIGKILL, power loss) at any point before the
// rename leaves the original file untouched — never a truncated or
// partially-written secret. os.Rename replacing an existing path never
// follows a symlink at the destination (unlike open()), so this is at least
// as safe against the TOCTOU class checkSecretFileTarget guards against.
func WriteSecretFile(path string, data []byte) error {
	if err := checkSecretFileTarget(path); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	ok = true
	return nil
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
	bound := big.NewInt(int64(len(alphabet)))
	b := make([]byte, n)
	for i := range b {
		v, err := rand.Int(rand.Reader, bound)
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

func ReadPidFile(dataDir string) (int, error) {
	data, err := os.ReadFile(PidFilePath(dataDir))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
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
	return acquireDaemonLock(dataDir, nil)
}

// acquireDaemonLock is AcquireDaemonLock with a hook that runs between opening
// the PID file and locking it, so the unlink-while-waiting race can be tested.
func acquireDaemonLock(dataDir string, afterOpen func()) (*DaemonLock, error) {
	for {
		f, err := lockPidFile(dataDir, afterOpen)
		if err != nil {
			return nil, err
		}
		if f == nil {
			continue // the file was replaced under us, start over
		}
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return nil, err
		}
		if _, err := f.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
			_ = f.Close()
			return nil, err
		}
		return &DaemonLock{f: f}, nil
	}
}

// lockPidFile opens and locks the PID file. It returns a nil file and no error
// when the previous owner removed the file between our open and our lock:
// locking that unlinked inode would let a third daemon create a fresh file and
// lock it too, so only a lock on the file now at path counts.
func lockPidFile(dataDir string, afterOpen func()) (*os.File, error) {
	path := PidFilePath(dataDir)
	if err := checkSecretFileTarget(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if afterOpen != nil {
		afterOpen()
	}
	if err := flockExclusive(f); err != nil {
		existingPid := "unknown"
		if pid, readErr := ReadPidFile(dataDir); readErr == nil {
			existingPid = strconv.Itoa(pid)
		}
		_ = f.Close()
		return nil, fmt.Errorf("a RunWisp daemon is already running for data dir %q (pid %s); stop it first", dataDir, existingPid)
	}
	if !isCurrentFile(f, path) {
		_ = f.Close()
		return nil, nil
	}
	return f, nil
}

// flockExclusive takes the lock without blocking. A PidFileLocked probe holds the
// lock for an instant, and a CLI waiting on a daemon that is starting up probes
// in a loop, so a busy lock is retried briefly before it counts as another daemon.
func flockExclusive(f *os.File) error {
	var err error
	for range 10 {
		if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return err
}

// PidFileLocked reports whether a daemon currently holds the lock on the PID
// file at path. The lock lives exactly as long as the daemon process, so unlike
// signalling the PID it can't be fooled by an unrelated process that was handed
// a dead daemon's PID, or by a renamed binary.
func PidFileLocked(path string) bool {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

// ProcessAlive reports whether a process with pid exists, via signal 0: the
// kernel delivers nothing but still reports ESRCH for a missing pid. EPERM
// means the process exists under another user (e.g. a root daemon checked by
// an unprivileged CLI), so it counts as alive. Works on Linux and macOS alike,
// unlike a /proc/<pid> stat.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// isCurrentFile reports whether f is still the file at path.
func isCurrentFile(f *os.File, path string) bool {
	held, err := f.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(path)
	return err == nil && os.SameFile(held, current)
}

// Release removes the PID file, then unlocks and closes it. The file goes first
// so the lock is held while it is removed: a daemon that locks the file after
// the unlock either finds it gone (and starts over, see acquireDaemonLock) or
// finds a file nobody removed. Best-effort: unexpected failures are logged via
// slog.Warn, never panicked.
func (l *DaemonLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	if err := os.Remove(l.f.Name()); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove PID file", "err", err)
	}
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		slog.Warn("Failed to release daemon lock", "err", err)
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
