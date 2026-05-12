// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/storage"
	srp "mz.attahri.com/code/srp/v3"
	"github.com/stretchr/testify/require"
)

// setupResetPasswordTempDir wires flags.DataDir to a fresh temp dir for the
// duration of t. The cleanup restores the previous values so subsequent
// tests don't see leakage.
func setupResetPasswordTempDir(t *testing.T) string {
	t.Helper()
	prev := flags
	t.Cleanup(func() { flags = prev })
	flags.DataDir = t.TempDir()
	return flags.DataDir
}

// TestResetPassword_AutoGeneratesAndStoresVerifier exercises the default
// path: no --password-stdin, so the command must generate a random password,
// print it, and write a verifier that the SRP server can validate against
// the same password.
func TestResetPassword_AutoGeneratesAndStoresVerifier(t *testing.T) {
	resetPasswordFlags.PasswordStdin = false
	dir := setupResetPasswordTempDir(t)

	// Pre-seed a DB so storage.New() doesn't have to migrate from zero.
	db, err := storage.New(filepath.Join(dir, "runwisp.db"), io.Discard, nil)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	var buf bytes.Buffer
	require.NoError(t, runResetPassword(&buf))

	// Extract the printed password — exactly one indented line.
	password := extractPrintedPassword(t, buf.String())

	// Re-open and verify the stored salt+verifier authenticate against the
	// printed password using the SRP handshake.
	db, err = storage.New(filepath.Join(dir, "runwisp.db"), io.Discard, nil)
	require.NoError(t, err)
	defer db.Close()

	verHex, ok, err := db.GetSecret(storage.ConfigKeySRPVerifier)
	require.NoError(t, err)
	require.True(t, ok)
	saltHex, ok, err := db.GetSecret(storage.ConfigKeySRPSalt)
	require.NoError(t, err)
	require.True(t, ok)

	verifier, err := hex.DecodeString(verHex)
	require.NoError(t, err)
	salt, err := hex.DecodeString(saltHex)
	require.NoError(t, err)

	server, err := srp.NewServer(datadir.SRPParams(), datadir.SRPIdentity, salt, verifier)
	require.NoError(t, err)
	client, err := srp.NewClient(datadir.SRPParams(), datadir.SRPIdentity, password, salt)
	require.NoError(t, err)
	require.NoError(t, client.SetB(server.B()))
	require.NoError(t, server.SetA(client.A()))
	M1, err := client.ComputeM1()
	require.NoError(t, err)
	ok, err = server.CheckM1(M1)
	require.NoError(t, err)
	require.True(t, ok, "printed password did not authenticate against the stored verifier")
}

// TestResetPassword_DropsJWTSecretAndEnvPasswordHash confirms the rotation
// invariants: existing sessions must die, and any cached env-password
// fingerprint becomes stale (it was keyed by the now-deleted JWT secret).
func TestResetPassword_DropsJWTSecretAndEnvPasswordHash(t *testing.T) {
	resetPasswordFlags.PasswordStdin = false
	dir := setupResetPasswordTempDir(t)

	dbPath := filepath.Join(dir, "runwisp.db")
	db, err := storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)
	require.NoError(t, db.SetSecret(storage.ConfigKeyJWTSecret, "stale-secret"))
	require.NoError(t, db.SetSecret(storage.ConfigKeyEnvPasswordSum, "stale-hash"))
	require.NoError(t, db.Close())

	var buf bytes.Buffer
	require.NoError(t, runResetPassword(&buf))

	db, err = storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)
	defer db.Close()

	_, found, err := db.GetSecret(storage.ConfigKeyJWTSecret)
	require.NoError(t, err)
	require.False(t, found, "JWT secret row must be deleted so the next boot rotates it")
	_, found, err = db.GetSecret(storage.ConfigKeyEnvPasswordSum)
	require.NoError(t, err)
	require.False(t, found, "env-password fingerprint row must be deleted so the next boot rebuilds it")
}

// TestResetPassword_StdinPath uses the operator-supplied password path and
// confirms the command stores a verifier that matches the piped value.
func TestResetPassword_StdinPath(t *testing.T) {
	resetPasswordFlags.PasswordStdin = true
	t.Cleanup(func() { resetPasswordFlags.PasswordStdin = false })
	dir := setupResetPasswordTempDir(t)

	dbPath := filepath.Join(dir, "runwisp.db")
	db, err := storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	withStdin(t, "operator-chose-this\n", func() {
		var buf bytes.Buffer
		require.NoError(t, runResetPassword(&buf))
		require.Contains(t, buf.String(), "Password updated")
		require.NotContains(t, buf.String(), "operator-chose-this",
			"piped password must not be echoed back to stdout")
	})

	db, err = storage.New(dbPath, io.Discard, nil)
	require.NoError(t, err)
	defer db.Close()

	saltHex, _, err := db.GetSecret(storage.ConfigKeySRPSalt)
	require.NoError(t, err)
	salt, err := hex.DecodeString(saltHex)
	require.NoError(t, err)
	want, err := datadir.DeriveSRPVerifier("operator-chose-this", salt)
	require.NoError(t, err)

	gotHex, _, err := db.GetSecret(storage.ConfigKeySRPVerifier)
	require.NoError(t, err)
	require.Equal(t, hex.EncodeToString(want), gotHex)
}

// extractPrintedPassword pulls the indented password line out of the
// success banner. Format: blank line, four-space indent + password, blank
// line.
func extractPrintedPassword(t *testing.T, banner string) string {
	t.Helper()
	for _, line := range strings.Split(banner, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(trimmed, "Password") && !strings.HasPrefix(trimmed, "Start") && !strings.HasPrefix(trimmed, "Save") {
			return trimmed
		}
	}
	t.Fatalf("could not extract password from banner: %q", banner)
	return ""
}

// withStdin temporarily redirects os.Stdin to a pipe seeded with the given
// payload. Restores the previous descriptor on cleanup.
func withStdin(t *testing.T, payload string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev; r.Close() })
	fn()
}
