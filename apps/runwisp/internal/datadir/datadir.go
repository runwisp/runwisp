// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package datadir

import (
	"crypto"
	"crypto/rand"
	"crypto/sha256" // also registers SHA-256 via init() for the crypto.Hash registry
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/crypto/pbkdf2"

	srp "mz.attahri.com/code/srp/v3"

	"log/slog"
)

// SecretStore reads and writes secret-keyed config rows. The store routes
// through the secret-encryption wrapper, so when RUNWISP_DATA_KEY is set the
// on-disk value carries the "enc:v1:" prefix and is transparently decrypted
// on read.
type SecretStore interface {
	GetSecret(key string) (string, bool, error)
	SetSecret(key, value string) error
	// DeleteConfigValue removes the row for key. Absent rows are no-ops.
	// Required to refuse-to-boot on legacy password rows.
	DeleteConfigValue(key string) error
}

// SRPIdentity is the fixed SRP username used by the single-operator
// daemon. RunWisp has no user model; the identity is a placeholder needed
// only to bind the verifier and proofs to a constant value. Must match
// the client-side constant in apps/ui/src/lib/api.ts.
const SRPIdentity = "runwisp"

// Local copies of the storage layer's key names. We duplicate them rather
// than import the storage package to avoid a circular dependency (storage
// imports nothing from datadir today).
const (
	passwordKey              = "password"
	srpVerifierKey           = "srp_verifier"
	srpSaltKey               = "srp_salt"
	srpVerifierHexLengthHint = 4096 / 8 * 2 // 1024 hex chars for a 4096-bit value
)

// pbkdf2Iterations is the RFC-recommended PBKDF2-SHA256 iteration count for
// password stretching circa 2025. Browser WebCrypto handles this in
// 300–500 ms; Go-side it's ~50–100 ms. Increasing this number breaks
// existing verifiers (login fails) — leave it pinned across releases.
const pbkdf2Iterations = 600_000

// pbkdf2KeyLen is the output size of the KDF. 32 bytes is sufficient
// entropy for SHA-256-based SRP.
const pbkdf2KeyLen = 32

// srpSaltBytes is the on-disk salt length. 16 bytes is the SRP library's
// recommended default and gives 2^128 distinct salts.
const srpSaltBytes = 16

// srpParams is the SRP parameter set used by every client/server pair:
// RFC 5054 group 16 (4096-bit), SHA-256, PBKDF2-SHA256 (600 000 iter).
// Must match the client-side params object exactly.
var srpParams = &srp.Params{
	Name:  "DH16-SHA256-PBKDF2-600k",
	Group: srp.RFC5054Group4096,
	Hash:  crypto.SHA256,
	KDF: func(username, password string, salt []byte) ([]byte, error) {
		// We don't bind the username into the KDF because the identity is a
		// fixed constant (SRPIdentity). Mixing it in would not increase
		// entropy and would create needless drift with the browser code.
		return pbkdf2.Key([]byte(password), salt, pbkdf2Iterations, pbkdf2KeyLen, sha256.New), nil
	},
}

// GenerateSRPSalt returns a fresh random salt for SRP credential derivation.
func GenerateSRPSalt() ([]byte, error) {
	b := make([]byte, srpSaltBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate SRP salt: %w", err)
	}
	return b, nil
}

// DeriveSRPVerifier computes the SRP-6a verifier v from password and salt
// using PBKDF2-SHA256 (600 000 iter) → x, then v = g^x mod N. Same
// computation the client performs internally; we expose it directly so the
// daemon can construct the verifier from an auto-generated password before
// the first login ever happens.
func DeriveSRPVerifier(password string, salt []byte) ([]byte, error) {
	triplet, err := srp.ComputeVerifier(srpParams, SRPIdentity, password, salt)
	if err != nil {
		return nil, fmt.Errorf("compute SRP verifier: %w", err)
	}
	return triplet.Verifier(), nil
}

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

// SRPParams returns the shared SRP parameter set. Callers should treat the
// returned pointer as read-only.
func SRPParams() *srp.Params { return srpParams }

// SRPCredentials holds the on-disk state needed to mount the SRP server side
// of authentication: the salt clients receive at login time and the verifier
// used to authenticate them. The raw operator password is *never* stored.
type SRPCredentials struct {
	Verifier []byte
	Salt     []byte
}

// ResolveSRPCredentials reads or generates the daemon's SRP credentials.
//
// Priority:
//  1. RUNWISP_PASSWORD env → derive verifier+salt in memory, do not persist
//     (env-var users have an external secrets system; persisting would
//     defeat its purpose).
//  2. Stored srp_verifier + srp_salt rows → use them.
//  3. Generate a fresh password + salt → derive a verifier, persist both
//     (encrypted at rest when a cipher is configured), return the
//     password so the caller can print it once.
//
// The returned generatedPassword is non-empty only on path 3. It is
// disclosed to the operator once on first boot ("save this now — it will
// not be shown again"); a password-fingerprint comparison in
// resolveJWTSecret tracks env-var password rotations.
//
// Refuses to boot if a legacy plaintext `password` row is present so a
// silent migration cannot strip security guarantees that the operator
// might rely on.
func ResolveSRPCredentials(store SecretStore) (creds SRPCredentials, generatedPassword string, err error) {
	if legacy, ok, lErr := store.GetSecret(passwordKey); lErr == nil && ok && legacy != "" {
		return SRPCredentials{}, "", fmt.Errorf("legacy plaintext password row present in SQLite; delete it (or set RUNWISP_PASSWORD) before upgrading — see CHANGELOG for migration notes")
	}

	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		salt, sErr := GenerateSRPSalt()
		if sErr != nil {
			return SRPCredentials{}, "", sErr
		}
		ver, vErr := DeriveSRPVerifier(envPw, salt)
		if vErr != nil {
			return SRPCredentials{}, "", vErr
		}
		return SRPCredentials{Verifier: ver, Salt: salt}, "", nil
	}

	storedVerifier, vFound, err := store.GetSecret(srpVerifierKey)
	if err != nil {
		return SRPCredentials{}, "", err
	}
	storedSalt, sFound, err := store.GetSecret(srpSaltKey)
	if err != nil {
		return SRPCredentials{}, "", err
	}
	if vFound && sFound && storedVerifier != "" && storedSalt != "" {
		ver, decErr := hex.DecodeString(storedVerifier)
		if decErr != nil {
			return SRPCredentials{}, "", fmt.Errorf("decode stored SRP verifier: %w", decErr)
		}
		salt, decErr := hex.DecodeString(storedSalt)
		if decErr != nil {
			return SRPCredentials{}, "", fmt.Errorf("decode stored SRP salt: %w", decErr)
		}
		return SRPCredentials{Verifier: ver, Salt: salt}, "", nil
	}

	// First boot: invent both. Print the password to stdout exactly once.
	pw, pwErr := GeneratePassword()
	if pwErr != nil {
		return SRPCredentials{}, "", pwErr
	}
	salt, sErr := GenerateSRPSalt()
	if sErr != nil {
		return SRPCredentials{}, "", sErr
	}
	ver, vErr := DeriveSRPVerifier(pw, salt)
	if vErr != nil {
		return SRPCredentials{}, "", vErr
	}
	if err := store.SetSecret(srpVerifierKey, hex.EncodeToString(ver)); err != nil {
		return SRPCredentials{}, "", err
	}
	if err := store.SetSecret(srpSaltKey, hex.EncodeToString(salt)); err != nil {
		return SRPCredentials{}, "", err
	}
	return SRPCredentials{Verifier: ver, Salt: salt}, pw, nil
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
