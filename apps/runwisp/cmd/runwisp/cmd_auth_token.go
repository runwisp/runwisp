// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/spf13/cobra"
)

var authTokenFlags struct {
	Remote   string
	Password string
}

// authTokenCmd performs a one-shot SRP login against a remote daemon and
// prints the resulting JWT to stdout. Replaces the deploy-hooks recipe that
// previously instructed operators to compose curl + sha256sum by hand.
var authTokenCmd = &cobra.Command{
	Use:   "auth-token",
	Short: "Authenticate against a remote daemon and print a session JWT",
	Long: `Performs an SRP login against the daemon at --remote and prints the
resulting JWT to stdout. Pipe the token into curl, an env file, or a deploy hook:

    export RUNWISP_REMOTE_PASSWORD='…'
    TOKEN=$(runwisp auth-token --remote https://runwisp.example.com --password env)
    curl -H "Authorization: Bearer $TOKEN" https://runwisp.example.com/api/info

Password sources:

  --password env       Read from $RUNWISP_REMOTE_PASSWORD (default).
  --password -         Read a single line from stdin (no echo).

The token is valid for 24 hours (the daemon's standard session TTL).`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAuthToken(cmd.OutOrStdout())
	},
}

func init() {
	authTokenCmd.Flags().StringVar(&authTokenFlags.Remote, "remote", "", "remote daemon base URL (e.g. https://runwisp.example.com)")
	authTokenCmd.Flags().StringVar(&authTokenFlags.Password, "password", "env", "password source: env|- (env reads $RUNWISP_REMOTE_PASSWORD; - reads stdin)")
	_ = authTokenCmd.MarkFlagRequired("remote")
	rootCmd.AddCommand(authTokenCmd)
}

func runAuthToken(out io.Writer) error {
	password, err := readRemotePasswordFrom("RUNWISP_REMOTE_PASSWORD", authTokenFlags.Password)
	if err != nil {
		return err
	}
	client := apiclient.New(authTokenFlags.Remote, password)
	if err := client.AuthenticateSRP(); err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return fmt.Errorf("authentication failed: invalid password for %s", authTokenFlags.Remote)
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return fmt.Errorf("rate limited by %s (too many recent attempts); back off and retry", authTokenFlags.Remote)
		}
		return fmt.Errorf("auth-token: %w", err)
	}
	tok := client.Token()
	if tok == "" {
		return errors.New("auth-token: empty token returned")
	}
	_, err = fmt.Fprintln(out, tok)
	return err
}
