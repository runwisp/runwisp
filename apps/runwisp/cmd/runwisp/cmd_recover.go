// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
	"github.com/spf13/cobra"
)

var recoverFlags struct {
	WipeSecrets bool
	Yes         bool
}

// recoverCmd is the escape hatch for a data dir whose secrets cannot be
// decrypted — typically because the operator lost RUNWISP_DATA_KEY. Without
// flags it prints a diagnostic report (which rows are encrypted, how many);
// with --wipe-secrets it deletes every row in storage.SecretKeys so the next
// daemon boot generates fresh credentials. Destructive but explicit.
var recoverCmd = &cobra.Command{
	Use:   "recover",
	Short: "Inspect or wipe encrypted secret rows when RUNWISP_DATA_KEY is lost",
	Long: `Inspect or recover a data directory whose at-rest secrets cannot be decrypted.

Without flags, prints a diagnostic report: how many secret rows are encrypted
at rest, and which key constants they belong to. Safe to run at any time.

With --wipe-secrets, deletes every row whose key is in storage.SecretKeys
(SRP verifier, SRP salt, JWT signing secret, env-password fingerprint,
legacy password). The next daemon boot will generate a fresh password and
JWT secret; web sessions are invalidated. The wipe is irreversible — the
operator must confirm at the terminal, or pass --yes for scripted use.

This command bypasses the daemon's normal refuse-to-boot-on-encrypted-but-no-
key check by talking to SQLite directly, so it works even when the data dir
cannot be unlocked.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runRecover(cmd.OutOrStdout(), cmd.InOrStdin())
	},
}

func init() {
	recoverCmd.Flags().BoolVar(&recoverFlags.WipeSecrets, "wipe-secrets", false, "delete every row in storage.SecretKeys (destructive)")
	recoverCmd.Flags().BoolVar(&recoverFlags.Yes, "yes", false, "skip the confirmation prompt (required when stdin is not a TTY)")
	rootCmd.AddCommand(recoverCmd)
}

func runRecover(out io.Writer, in io.Reader) error {
	if err := refuseIfDaemonRunning(); err != nil {
		return err
	}

	// Open SQLite directly: storage.New() refuses to start when secrets are
	// encrypted but RUNWISP_DATA_KEY is absent, which is precisely the
	// scenario this command exists to recover from.
	db, err := sql.Open("sqlite", flags.DBPath())
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	encryptedKeys, plaintextKeys, err := classifySecretRows(db)
	if err != nil {
		return err
	}

	if !recoverFlags.WipeSecrets {
		return printRecoverReport(out, encryptedKeys, plaintextKeys)
	}

	if !recoverFlags.Yes {
		if err := confirmWipe(out, in); err != nil {
			return err
		}
	}

	n, err := wipeSecretRows(db)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Wiped %d secret rows. The next daemon boot will generate a fresh password and JWT secret.\n", n)
	return nil
}

// classifySecretRows reads every row whose key is in storage.SecretKeys and
// reports which ones are encrypted at rest vs plaintext. The classification
// is purely lexical (secretcipher.IsEncrypted checks the "enc:v1:" prefix),
// which is enough to tell the operator what the recovery path will touch.
func classifySecretRows(db *sql.DB) (encrypted, plaintext []string, err error) {
	keys := make([]string, 0, len(storage.SecretKeys))
	for k := range storage.SecretKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		var val string
		err := db.QueryRow(`SELECT value FROM config_entries WHERE key = ?`, key).Scan(&val)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("read %q: %w", key, err)
		}
		if secretcipher.IsEncrypted(val) {
			encrypted = append(encrypted, key)
		} else {
			plaintext = append(plaintext, key)
		}
	}
	return encrypted, plaintext, nil
}

func printRecoverReport(out io.Writer, encrypted, plaintext []string) error {
	fmt.Fprintf(out, "Data directory: %s\n", flags.DataDir)
	fmt.Fprintf(out, "Database:       %s\n\n", flags.DBPath())

	if len(encrypted) == 0 && len(plaintext) == 0 {
		fmt.Fprintln(out, "No secret rows present. The next daemon boot will seed fresh credentials.")
		return nil
	}

	if len(encrypted) > 0 {
		fmt.Fprintf(out, "Encrypted at rest (%d):\n", len(encrypted))
		for _, k := range encrypted {
			fmt.Fprintf(out, "  - %s\n", k)
		}
		fmt.Fprintln(out)
		fmt.Fprintln(out, "These rows cannot be decrypted without the correct RUNWISP_DATA_KEY.")
		fmt.Fprintln(out, "If the key is lost, re-run with --wipe-secrets to delete them and regenerate")
		fmt.Fprintln(out, "fresh credentials on the next daemon boot.")
	}

	if len(plaintext) > 0 {
		if len(encrypted) > 0 {
			fmt.Fprintln(out)
		}
		fmt.Fprintf(out, "Plaintext (%d):\n", len(plaintext))
		for _, k := range plaintext {
			fmt.Fprintf(out, "  - %s\n", k)
		}
	}
	return nil
}

// confirmWipe blocks for an explicit "yes" on stdin when the user has a TTY.
// Without a TTY (cron, CI, piped scripts) we require --yes up front so a
// stray stdin doesn't trigger an irreversible delete.
func confirmWipe(out io.Writer, in io.Reader) error {
	if !isTerminal(in) {
		return fmt.Errorf("--wipe-secrets without a TTY requires --yes for confirmation")
	}

	fmt.Fprintln(out, "About to delete every encrypted secret row from", flags.DBPath()+".")
	fmt.Fprintln(out, "This is irreversible. The daemon will generate a fresh password on next boot.")
	fmt.Fprint(out, `Type "yes" to continue: `)

	r := bufio.NewReader(in)
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if strings.TrimSpace(line) != "yes" {
		return fmt.Errorf("aborted: confirmation did not match")
	}
	return nil
}

// isTerminal reports whether r is a stdin handle attached to a real TTY,
// using the same os.File character-device probe stdlib's `tabwriter` and
// other terminal heuristics use. Avoids pulling in golang.org/x/term for
// a one-off check.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// wipeSecretRows deletes every row whose key is in storage.SecretKeys. The
// rows are removed in one transaction so a crash mid-wipe leaves the
// database in a consistent state — either every secret is gone or none are.
func wipeSecretRows(db *sql.DB) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin wipe tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	keys := make([]string, 0, len(storage.SecretKeys))
	for k := range storage.SecretKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	total := 0
	for _, key := range keys {
		res, err := tx.Exec(`DELETE FROM config_entries WHERE key = ?`, key)
		if err != nil {
			return 0, fmt.Errorf("delete %q: %w", key, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, err
		}
		total += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit wipe tx: %w", err)
	}
	return total, nil
}
