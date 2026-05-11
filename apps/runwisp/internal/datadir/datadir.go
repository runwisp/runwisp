// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"crypto/rand"
	"encoding/base64"
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

// PasswordStore reads and writes the daemon password from persistent storage.
// The plaintext password must be retrievable by the server to verify
// CHAP challenge-response logins (sha256(password + nonce)), so the SQLite
// `config_entries` row holds the cleartext value protected by the data dir's
// 0700 perms (the directory only the daemon's user can read).
type PasswordStore interface {
	GetConfigValue(key string) (string, bool, error)
	SetConfigValue(key, value string) error
}

// passwordKey duplicates storage.ConfigKeyPassword. We keep a local copy
// rather than importing storage to avoid a circular dependency: storage
// imports nothing from datadir today, and we want to keep it that way.
const passwordKey = "password"

// EnsureDir creates dir (and parents) with mode 0700 so that secrets stored
// inside are not exposed to other local users via directory traversal.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0700)
}

// writeFileNoFollow writes data to path with mode 0600, refusing to follow
// symlinks. If path already exists, it must be a regular file owned by the
// caller; otherwise the write is rejected. This prevents a TOCTOU symlink
// attack where another local user replaces a data-dir file with a symlink to
// a sensitive target the daemon can write (e.g. ~/.ssh/authorized_keys).
func writeFileNoFollow(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to write %s: path is a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to write %s: not a regular file", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
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

// GenerateJWTSecret returns a cryptographically random base64-encoded 32-byte secret.
func GenerateJWTSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
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

// ResolvePassword reads or generates the daemon password.
// Priority: RUNWISP_PASSWORD env > SQLite config row > generate new (and persist).
// Returns isNew=true only when a fresh password was generated.
//
// When the env var is set, the value is used in-memory only — it is NOT
// written back to SQLite. Operators using Docker secrets, systemd
// LoadCredential, or sealed-secrets do so specifically to keep credentials
// out of the data directory; persisting the env var would silently defeat
// that. The TUI and CLI must read RUNWISP_PASSWORD themselves in those
// shells; falling back to the stored value from a different shell would be
// misleading anyway.
func ResolvePassword(store PasswordStore) (password string, isNew bool, err error) {
	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		return envPw, false, nil
	}

	existing, found, err := store.GetConfigValue(passwordKey)
	if err != nil {
		return "", false, err
	}
	if found && existing != "" {
		return existing, false, nil
	}

	pw, genErr := GeneratePassword()
	if genErr != nil {
		return "", false, genErr
	}
	if err := store.SetConfigValue(passwordKey, pw); err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// PidFilePath returns the path to the daemon PID file.
func PidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.pid")
}

func WritePidFile(dataDir string) error {
	return writeFileNoFollow(PidFilePath(dataDir), []byte(strconv.Itoa(os.Getpid())+"\n"))
}

func ReadPidFile(dataDir string) (int, error) {
	data, err := os.ReadFile(PidFilePath(dataDir))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// CleanPidFile removes the PID file.
func CleanPidFile(dataDir string) {
	if err := os.Remove(PidFilePath(dataDir)); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove PID file", "err", err)
	}
}
