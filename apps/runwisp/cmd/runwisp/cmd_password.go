// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
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
	passwordExitNoAuth        = 5 // daemon runs with RUNWISP_AUTH=off — no password exists
)

var passwordCmd = &cobra.Command{
	Use: "password",
	// Only reads from a daemon, so it must not create ./.runwisp the way the
	// root hook does for commands that write there.
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error { return resolveFlags(cmd) },
	Short:             "Print the daemon's ephemeral password (--viewer: the read-only one)",
	Long: `runwisp password — retrieve the ephemeral daemon password.

Prints the in-memory ephemeral password to stdout. Use this when you need to
paste the password into a browser on another device.

With --viewer, prints the read-only password instead, generating a random one
(in memory, gone on restart) if none is set yet. Sessions opened with it can
view everything and change nothing. Set RUNWISP_VIEWER_PASSWORD on the daemon
for a stable one.

SECURITY: The password is written to stdout in plaintext. It will land in
your shell scrollback, in any pipe target, and in journald if your shell's
stdout is captured. Pipe to a clipboard tool to keep it out of scrollback:

    runwisp password | wl-copy
    runwisp password | pbcopy
    runwisp password | xclip -selection clipboard

Fails when the daemon is configured with RUNWISP_PASSWORD (or, with --viewer,
RUNWISP_VIEWER_PASSWORD) — that value is never disclosed via this command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		socketPath := localAPISocketPath(flags)
		client := apiclient.NewUnix(socketPath)
		fetch, envVar := client.GetLocalCredentials, "RUNWISP_PASSWORD"
		if viewer, _ := cmd.Flags().GetBool("viewer"); viewer {
			fetch, envVar = client.MintViewerPassword, "RUNWISP_VIEWER_PASSWORD"
		}
		code := runPasswordVia(cmd.Context(), os.Stdout, os.Stderr, fetch, socketPath, envVar, daemonNotRunningHint(flags))
		if code != passwordExitOK {
			os.Exit(code)
		}
		return nil
	},
}

func init() {
	passwordCmd.Flags().Bool("viewer", false, "print the read-only password instead (generated on first use)")
}

// credentialsFetcher is the slice of apiclient.Client that cmd_password
// exercises. Splitting it out keeps the unit test isolated from a real socket.
type credentialsFetcher interface {
	GetLocalCredentials(ctx context.Context) (*server.LocalCredentialsBody, error)
}

func runPassword(ctx context.Context, stdout, stderr io.Writer, client credentialsFetcher, socketPath string) int {
	return runPasswordVia(ctx, stdout, stderr, client.GetLocalCredentials, socketPath, "RUNWISP_PASSWORD", startDaemonHint)
}

// runPasswordVia prints the password fetch returns (the admin or the read-only
// one). envVar names the variable that would have supplied it, for the refusal
// message; notRunningHint is the next step when no daemon answers.
func runPasswordVia(ctx context.Context, stdout, stderr io.Writer, fetch func(context.Context) (*server.LocalCredentialsBody, error), socketPath, envVar, notRunningHint string) int {
	creds, err := fetch(ctx)
	if err == nil {
		fmt.Fprintln(stdout, creds.Password)
		return passwordExitOK
	}

	if errors.Is(err, apiclient.ErrLocalCredentialsUnavailable) {
		renderError(stderr,
			fmt.Sprintf("this daemon is configured with %s; RunWisp will not disclose it", envVar),
			"Retrieve it from wherever you set it (Docker secret, systemd credential, vault, password manager, …).",
			"")
		return passwordExitRefused
	}
	if errors.Is(err, apiclient.ErrAuthDisabled) {
		renderError(stderr,
			"this daemon runs with RUNWISP_AUTH=off — authentication is disabled, so there is no password",
			"", "")
		return passwordExitNoAuth
	}
	if apiclient.IsHTTPStatus(err, http.StatusForbidden) {
		renderError(stderr, "internal error: socket request was not local-trusted", "", "")
		return passwordExitInternalGate
	}
	if isTransportError(err) {
		renderError(stderr,
			fmt.Sprintf("daemon not running at %s: %v", socketPath, err),
			fmt.Sprintf("The ephemeral password only exists while a daemon is up — %s.", notRunningHint),
			"")
		return passwordExitUnreachable
	}
	renderError(stderr, fmt.Sprintf("unexpected error: %v", err), "", "")
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
