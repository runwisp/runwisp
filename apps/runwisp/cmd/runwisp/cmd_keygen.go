// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/runwisp/runwisp/internal/storage/secretcipher"
	"github.com/spf13/cobra"
)

var keygenCmd = &cobra.Command{
	Use:   "keygen",
	Short: "Generate a random RUNWISP_DATA_KEY",
	Long: `Prints a base64-encoded 32-byte key suitable for use as RUNWISP_DATA_KEY.

When RUNWISP_DATA_KEY is set, the daemon AES-256-GCM-encrypts every secret
stored in the data dir (SRP verifier, JWT signing secret, password fingerprint).
Without that key the data dir cannot be unlocked — store the value in your
secrets manager, not next to the data dir it protects.

Example:

    export RUNWISP_DATA_KEY=$(runwisp keygen)
    runwisp
`,
	Args:                  cobra.NoArgs,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		buf := make([]byte, secretcipher.KeySize)
		if _, err := rand.Read(buf); err != nil {
			return fmt.Errorf("generate key: %w", err)
		}
		_, err := fmt.Fprintln(cmd.OutOrStdout(), base64.StdEncoding.EncodeToString(buf))
		return err
	},
}

func init() {
	rootCmd.AddCommand(keygenCmd)
}
