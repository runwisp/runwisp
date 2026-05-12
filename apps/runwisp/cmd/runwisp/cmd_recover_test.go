// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"database/sql"
	"io"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
	"github.com/stretchr/testify/require"
)

// setupRecoverTempDir wires flags.DataDir to a temp dir for the duration of
// t and returns the seeded SQLite path.
func setupRecoverTempDir(t *testing.T) string {
	t.Helper()
	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.DataDir = t.TempDir()
	return flags.DBPath()
}

// seedSecretRows opens the bare SQLite file, creates the config_entries
// table the daemon would have created, and writes one row per
// (key,value) entry. Used to fake encrypted rows in recovery tests without
// going through storage.New() (which we are explicitly testing the bypass
// of).
func seedSecretRows(t *testing.T, dbPath string, rows map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS config_entries (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	require.NoError(t, err)
	for k, v := range rows {
		_, err := db.Exec(`INSERT INTO config_entries (key, value) VALUES (?, ?)`, k, v)
		require.NoError(t, err)
	}
}

// TestRecover_ReportNamesEncryptedRows confirms the no-flag path prints a
// per-key inventory the operator can read without losing data.
func TestRecover_ReportNamesEncryptedRows(t *testing.T) {
	recoverFlags.WipeSecrets = false
	recoverFlags.Yes = false
	dbPath := setupRecoverTempDir(t)

	seedSecretRows(t, dbPath, map[string]string{
		storage.ConfigKeyJWTSecret:   secretcipher.Prefix + "stub-ciphertext",
		storage.ConfigKeySRPVerifier: secretcipher.Prefix + "stub-ciphertext",
		storage.ConfigKeySRPSalt:     "plaintext-salt",
	})

	var out bytes.Buffer
	require.NoError(t, runRecover(&out, &bytes.Buffer{}))

	got := out.String()
	require.Contains(t, got, storage.ConfigKeyJWTSecret)
	require.Contains(t, got, storage.ConfigKeySRPVerifier)
	require.Contains(t, got, storage.ConfigKeySRPSalt)
	require.Contains(t, got, "Encrypted at rest (2)")
	require.Contains(t, got, "Plaintext (1)")
}

// TestRecover_WipeSecretsDeletesRows is the destructive path: --wipe-secrets
// + --yes must remove every storage.SecretKeys row in one shot.
func TestRecover_WipeSecretsDeletesRows(t *testing.T) {
	recoverFlags.WipeSecrets = true
	recoverFlags.Yes = true
	t.Cleanup(func() { recoverFlags.WipeSecrets = false; recoverFlags.Yes = false })

	dbPath := setupRecoverTempDir(t)
	seedSecretRows(t, dbPath, map[string]string{
		storage.ConfigKeyJWTSecret:      secretcipher.Prefix + "stub",
		storage.ConfigKeyEnvPasswordSum: secretcipher.Prefix + "stub",
		storage.ConfigKeySRPVerifier:    secretcipher.Prefix + "stub",
		storage.ConfigKeySRPSalt:        secretcipher.Prefix + "stub",
		// A non-secret row must survive the wipe.
		storage.ConfigKeyFingerprint: "fp-must-stay",
	})

	var out bytes.Buffer
	require.NoError(t, runRecover(&out, &bytes.Buffer{}))

	got := out.String()
	require.Contains(t, got, "Wiped 4 secret rows")

	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	defer db.Close()

	for _, key := range []string{
		storage.ConfigKeyJWTSecret,
		storage.ConfigKeyEnvPasswordSum,
		storage.ConfigKeySRPVerifier,
		storage.ConfigKeySRPSalt,
	} {
		var v string
		err := db.QueryRow(`SELECT value FROM config_entries WHERE key = ?`, key).Scan(&v)
		require.ErrorIs(t, err, sql.ErrNoRows, "row %q should be deleted", key)
	}

	// Non-secret row must remain untouched.
	var fp string
	err = db.QueryRow(`SELECT value FROM config_entries WHERE key = ?`, storage.ConfigKeyFingerprint).Scan(&fp)
	require.NoError(t, err)
	require.Equal(t, "fp-must-stay", fp)
}

// TestRecover_RefusesWithoutTTYAndYes confirms a piped invocation cannot
// silently wipe — passing --wipe-secrets without --yes when stdin is not a
// TTY is rejected up front.
func TestRecover_RefusesWithoutTTYAndYes(t *testing.T) {
	recoverFlags.WipeSecrets = true
	recoverFlags.Yes = false
	t.Cleanup(func() { recoverFlags.WipeSecrets = false; recoverFlags.Yes = false })

	dbPath := setupRecoverTempDir(t)
	seedSecretRows(t, dbPath, map[string]string{
		storage.ConfigKeyJWTSecret: secretcipher.Prefix + "stub",
	})

	var out bytes.Buffer
	// A bytes.Buffer is not an *os.File, so isTerminal() returns false.
	err := runRecover(&out, &bytes.Buffer{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "--yes")
}

// TestRecover_EmptyDatabaseIsHandled confirms a fresh-but-empty data dir
// (only the schema, no rows) reports cleanly instead of failing.
func TestRecover_EmptyDatabaseIsHandled(t *testing.T) {
	recoverFlags.WipeSecrets = false
	recoverFlags.Yes = false

	dbPath := setupRecoverTempDir(t)
	// Touch the file so sql.Open succeeds against a real path. The classify
	// step expects config_entries to exist; create it empty.
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE config_entries (key TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	var out bytes.Buffer
	require.NoError(t, runRecover(&out, &bytes.Buffer{}))
	got := out.String()
	require.True(t,
		strings.Contains(got, "No secret rows present") || strings.Contains(got, "Plaintext (0)"),
		"unexpected report: %q", got)
}

// Smoke: io.Discard is used inside the command for the unused logOutput
// arg. Keeping the import used silences "imported and not used" when the
// test file is the only consumer in a future edit. (Documentation; the
// constant fires at compile time, not runtime.)
var _ = io.Discard
