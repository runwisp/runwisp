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
// Priority: RUNWISP_PASSWORD env > data/password file > generate new.
// Returns isNew=true only when a fresh password was generated.
func ResolvePassword(dataDir string) (password string, isNew bool, err error) {
	pwPath := filepath.Join(dataDir, "password")

	// Env var takes priority and is persisted for reconnect.
	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		if writeErr := writeSecretFile(pwPath, envPw); writeErr != nil {
			return "", false, writeErr
		}
		return envPw, false, nil
	}

	// Read existing or generate new via the shared helper.
	existing, _ := readSecretFile(pwPath)
	if existing != "" {
		return existing, false, nil
	}

	pw, genErr := GeneratePassword()
	if genErr != nil {
		return "", false, genErr
	}
	if writeErr := writeSecretFile(pwPath, pw); writeErr != nil {
		return "", false, writeErr
	}
	return pw, true, nil
}

// CleanPasswordFile removes the stored password file.
func CleanPasswordFile(dataDir string) {
	if err := os.Remove(filepath.Join(dataDir, "password")); err != nil && !os.IsNotExist(err) {
		slog.Warn("Failed to remove password file", "err", err)
	}
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

// readSecretFile reads and trims a single-value secret file.
func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeSecretFile persists a secret value with mode 0600. Refuses to follow
// symlinks; see writeFileNoFollow.
func writeSecretFile(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return writeFileNoFollow(path, []byte(value+"\n"))
}
