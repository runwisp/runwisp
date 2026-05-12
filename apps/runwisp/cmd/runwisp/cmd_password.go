// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/spf13/cobra"
)

// Exit codes for `runwisp password`. These are referenced by docs and shell
// integrations, so treat them as part of the CLI contract.
const (
	passwordExitOK            = 0
	passwordExitRefused       = 1 // daemon is using RUNWISP_PASSWORD
	passwordExitUnreachable   = 2 // socket missing, transport error
	passwordExitInternalGate  = 3 // 403 from a socket request — should not happen
	passwordExitUnexpectedErr = 4 // anything else
)

var passwordCmd = &cobra.Command{
	Use:   "password",
	Short: "Print the daemon's ephemeral password",
	Long: `runwisp password — retrieve the ephemeral daemon password.

Prints the in-memory ephemeral password to stdout. Use this when you need to
paste the password into a browser on another device.

SECURITY: The password is written to stdout in plaintext. It will land in
your shell scrollback, in any pipe target, and in journald if your shell's
stdout is captured. Pipe to a clipboard tool to keep it out of scrollback:

    runwisp password | wl-copy
    runwisp password | pbcopy
    runwisp password | xclip -selection clipboard

Fails when the daemon is configured with RUNWISP_PASSWORD — that value is
never disclosed via this command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := apiclient.NewUnix(localAPISocketPath())
		code := runPassword(os.Stdout, os.Stderr, client, localAPISocketPath())
		if code != passwordExitOK {
			os.Exit(code)
		}
		return nil
	},
}

// credentialsFetcher is the slice of apiclient.Client that cmd_password
// exercises. Splitting it out keeps the unit test isolated from a real socket.
type credentialsFetcher interface {
	GetLocalCredentials() (*apiclient.LocalCredentials, error)
}

func runPassword(stdout, stderr io.Writer, client credentialsFetcher, socketPath string) int {
	creds, err := client.GetLocalCredentials()
	if err == nil {
		fmt.Fprintln(stdout, creds.Password)
		return passwordExitOK
	}

	if errors.Is(err, apiclient.ErrLocalCredentialsUnavailable) {
		fmt.Fprintln(stderr, "Error: this daemon is configured with RUNWISP_PASSWORD; RunWisp will not disclose it.")
		fmt.Fprintln(stderr, "Retrieve it from wherever you set it (Docker secret, systemd credential, vault, password manager, …).")
		return passwordExitRefused
	}
	if apiclient.IsHTTPStatus(err, http.StatusForbidden) {
		fmt.Fprintln(stderr, "internal error: socket request was not local-trusted")
		return passwordExitInternalGate
	}
	if isTransportError(err) {
		fmt.Fprintf(stderr, "daemon not running at %s: %v\n", socketPath, err)
		return passwordExitUnreachable
	}
	fmt.Fprintf(stderr, "unexpected error: %v\n", err)
	return passwordExitUnexpectedErr
}

// isTransportError reports whether the error came from a failed dial or read
// (as opposed to an HTTP-level status). The apiclient wraps dial failures as
// "request failed: …", which is the marker we rely on; status errors are
// distinguishable via *apiclient.HTTPStatusError.
func isTransportError(err error) bool {
	var se *apiclient.HTTPStatusError
	return !errors.As(err, &se)
}
