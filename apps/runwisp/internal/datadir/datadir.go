// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/log"
)

func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// GenerateJWTSecret returns a cryptographically random base64-encoded 32-byte secret.
func GenerateJWTSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key), nil
}

// GeneratePassword returns a cryptographically random base62 password (128+ bits of entropy).
func GeneratePassword() (string, error) {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, 22)
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("failed to generate random password: %w", err)
		}
		b[i] = alphabet[n.Int64()]
	}
	return string(b), nil
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
		log.Warn("Failed to remove password file", "err", err)
	}
}

// PidFilePath returns the path to the daemon PID file.
func PidFilePath(dataDir string) string {
	return filepath.Join(dataDir, "daemon.pid")
}

func WritePidFile(dataDir string) error {
	return os.WriteFile(PidFilePath(dataDir), []byte(strconv.Itoa(os.Getpid())+"\n"), 0600)
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
		log.Warn("Failed to remove PID file", "err", err)
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

// writeSecretFile persists a secret value with mode 0600.
func writeSecretFile(path, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(value+"\n"), 0600)
}
