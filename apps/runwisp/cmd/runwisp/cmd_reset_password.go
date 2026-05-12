// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
	"github.com/spf13/cobra"
)

var resetPasswordFlags struct {
	PasswordStdin bool
}

// resetPasswordCmd writes a fresh SRP verifier+salt pair to SQLite. Without
// `--password-stdin` a new random password is generated and printed to
// stdout; with it, a single line is read from stdin and used. Either path
// invalidates all existing sessions by also deleting the JWT secret.
var resetPasswordCmd = &cobra.Command{
	Use:   "reset-password",
	Short: "Reset the daemon's operator password",
	Long: `Writes a fresh SRP verifier and salt to the daemon's SQLite database, then
deletes the JWT signing secret so the next daemon boot rotates it and all
existing sessions are invalidated.

The daemon must be stopped first — a running daemon caches the verifier in
memory and would keep accepting the old password until restarted.

Password sources:

  (default)             Generate a random base62 password and print it to
                        stdout. The operator stores the printed value.
  --password-stdin      Read a single line from stdin and use it verbatim.

If the data dir is encrypted at rest, set RUNWISP_DATA_KEY so the new
verifier is written under the same cipher as everything else.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runResetPassword(cmd.OutOrStdout())
	},
}

func init() {
	resetPasswordCmd.Flags().BoolVar(&resetPasswordFlags.PasswordStdin, "password-stdin", false, "read the new password as a single line from stdin (otherwise a random one is generated)")
	rootCmd.AddCommand(resetPasswordCmd)
}

func runResetPassword(out io.Writer) error {
	if err := refuseIfDaemonRunning(); err != nil {
		return err
	}

	cipher, err := secretcipher.FromEnv()
	if err != nil {
		return err
	}

	db, err := storage.New(flags.DBPath(), io.Discard, cipher)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	password, supplied, err := resolveResetPassword()
	if err != nil {
		return err
	}

	salt, err := datadir.GenerateSRPSalt()
	if err != nil {
		return err
	}
	verifier, err := datadir.DeriveSRPVerifier(password, salt)
	if err != nil {
		return err
	}

	if err := db.SetSecret(storage.ConfigKeySRPVerifier, hex.EncodeToString(verifier)); err != nil {
		return fmt.Errorf("write SRP verifier: %w", err)
	}
	if err := db.SetSecret(storage.ConfigKeySRPSalt, hex.EncodeToString(salt)); err != nil {
		return fmt.Errorf("write SRP salt: %w", err)
	}
	if err := db.DeleteConfigValue(storage.ConfigKeyJWTSecret); err != nil {
		return fmt.Errorf("rotate JWT secret: %w", err)
	}
	// Also drop the env-password fingerprint — if the operator was relying on
	// RUNWISP_PASSWORD, the next boot must re-derive its fingerprint under
	// the freshly-rotated JWT secret instead of comparing against a stale
	// HMAC keyed by the previous one.
	if err := db.DeleteConfigValue(storage.ConfigKeyEnvPasswordSum); err != nil {
		return fmt.Errorf("drop env-password fingerprint: %w", err)
	}

	if supplied {
		fmt.Fprintln(out, "Password updated. Start the daemon to apply the new credentials.")
	} else {
		fmt.Fprintln(out, "Password reset. Save this value — it will not be shown again:")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "    "+password)
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Start the daemon to apply the new credentials.")
	}
	return nil
}

// resolveResetPassword returns the password to derive the verifier from and
// whether the operator supplied it (true) or it was auto-generated (false).
func resolveResetPassword() (string, bool, error) {
	if !resetPasswordFlags.PasswordStdin {
		pw, err := datadir.GeneratePassword()
		if err != nil {
			return "", false, fmt.Errorf("generate password: %w", err)
		}
		return pw, false, nil
	}
	pw, err := readRemotePasswordFrom("RUNWISP_PASSWORD", "-")
	if err != nil {
		return "", false, err
	}
	return pw, true, nil
}

// refuseIfDaemonRunning returns a user-facing error when a daemon is alive
// on this data dir. Resetting credentials while the daemon is running races
// the in-memory verifier and leaves the operator unable to log in until a
// restart.
func refuseIfDaemonRunning() error {
	pidPath := datadir.PidFilePath(flags.DataDir)
	pid, err := datadir.ReadPidFile(flags.DataDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		// A malformed PID file is suspicious but not blocking; treat it as
		// "no daemon" so the reset can proceed on a half-broken data dir.
		return nil
	}
	if !processAlive(pid, pidPath) {
		return nil
	}
	return fmt.Errorf("a daemon is running (pid %d) on data dir %q; stop it first, then re-run reset-password", pid, flags.DataDir)
}
