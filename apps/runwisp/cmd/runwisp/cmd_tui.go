// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var tuiFlags struct {
	URL string
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Connect a TUI to a running daemon",
	Long: `Launches the interactive terminal UI and connects it to an already-running RunWisp daemon.

By default it connects to a local daemon over its Unix socket — no password
required, access is gated by data-dir filesystem permissions.

With --url (or RUNWISP_URL) set, the TUI connects to a daemon over HTTP — a
remote host, a container, or any daemon you don't share a data dir with. It
logs in via CHAP and caches the session token so you only enter the password
once. The password is read from RUNWISP_PASSWORD or, failing that, prompted for
without echo. A daemon started with RUNWISP_NO_AUTH needs no password.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUIClient(flags)
	},
}

func init() {
	tuiCmd.Flags().StringVar(&tuiFlags.URL, "url", "", "connect to a daemon over HTTP at this base URL instead of the local socket (env: RUNWISP_URL)")
}

func runTUIClient(f Flags) error {
	if remoteURL := cmp.Or(tuiFlags.URL, os.Getenv("RUNWISP_URL")); remoteURL != "" {
		return runTUIViaRemote(remoteURL, f)
	}

	client := apiclient.NewUnix(localAPISocketPath(f))

	if err := pollHealth(client, 5*time.Second); err != nil {
		return fmt.Errorf("cannot reach daemon at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint)
	}

	err := runTUIConnect(client, f)
	if err != nil && errors.Is(err, apiclient.ErrRateLimited) {
		return authRateLimitedError(f.Port)
	}
	return err
}

// maxRemotePasswordPrompts bounds the interactive re-prompt loop so a wrong
// password doesn't trap the operator; env-var and non-terminal auth are always
// single-shot.
const maxRemotePasswordPrompts = 3

// runTUIViaRemote attaches the TUI to a daemon over HTTP. It probes health and
// whether auth is required (so a RUNWISP_NO_AUTH daemon connects with none),
// authenticates via CHAP when needed (reusing a cached JWT), then hands the
// authenticated client to the shared TUI launch path.
func runTUIViaRemote(baseURL string, f Flags) error {
	probe := apiclient.New(baseURL, "")

	// Health is a public endpoint — probe it before auth so an unreachable
	// daemon reports as such rather than as a login failure.
	if err := probe.HealthCheck(); err != nil {
		return remoteUnreachableError(baseURL, err)
	}

	status, err := probe.AuthStatus()
	if err != nil {
		return fmt.Errorf("check authentication status at %s: %w", baseURL, err)
	}

	client := probe
	if status.AuthRequired {
		authed, authErr := authenticateRemoteTUI(baseURL)
		if authErr != nil {
			return authErr
		}
		client = authed
	}

	return launchConnectedTUI(client, tuiConnectMode{remote: true, connBaseURL: baseURL})
}

// authenticateRemoteTUI returns an authenticated client for baseURL. It reuses a
// cached session token when one is valid; otherwise it resolves a password
// (RUNWISP_PASSWORD or a no-echo prompt) and runs the CHAP handshake, caching
// the resulting token and re-prompting on a wrong password when interactive.
func authenticateRemoteTUI(baseURL string) (*apiclient.Client, error) {
	if cached := loadCachedToken(baseURL); cached != "" {
		client := apiclient.New(baseURL, "")
		client.SetToken(cached)
		return client, nil
	}

	envPassword := os.Getenv("RUNWISP_PASSWORD")
	interactive := isInteractiveTerminal()

	for attempt := 0; attempt < maxRemotePasswordPrompts; attempt++ {
		password, err := resolveRemotePassword(baseURL, envPassword, interactive)
		if err != nil {
			return nil, err
		}

		client := apiclient.New(baseURL, password)
		authErr := client.Authenticate()
		if authErr == nil {
			storeCachedToken(baseURL, client.Token())
			return client, nil
		}

		switch {
		case errors.Is(authErr, apiclient.ErrUnauthorized):
			// Only an interactive prompt is worth retrying; an env-var password
			// is fixed, so fail fast rather than loop on the same value.
			if interactive && envPassword == "" && attempt+1 < maxRemotePasswordPrompts {
				fmt.Fprintln(os.Stderr, "Incorrect password — try again.")
				continue
			}
			return nil, remoteAuthFailedError(baseURL)
		case errors.Is(authErr, apiclient.ErrRateLimited):
			return nil, remoteRateLimitedError(baseURL)
		default:
			return nil, fmt.Errorf("authenticate with %s: %w", baseURL, authErr)
		}
	}

	return nil, remoteAuthFailedError(baseURL)
}

// resolveRemotePassword yields the password for the CHAP handshake: the
// RUNWISP_PASSWORD env var when set, else a no-echo terminal prompt. A
// non-interactive session with no env var is a hard error — there is nowhere
// safe to read the password from.
func resolveRemotePassword(baseURL, envPassword string, interactive bool) (string, error) {
	if envPassword != "" {
		return envPassword, nil
	}
	if !interactive {
		return "", &userFacingError{
			title:   fmt.Sprintf("a password is required to connect to %s", baseURL),
			details: "Set RUNWISP_PASSWORD in the environment, or run 'runwisp tui --url ...' from an interactive terminal so it can prompt you.",
		}
	}
	return promptPassword(fmt.Sprintf("Password for %s: ", baseURL))
}

// promptPassword reads a line from the terminal without echoing it. Writing the
// prompt and trailing newline to stderr keeps stdout clean for any callers that
// capture it.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimSpace(string(pw)), nil
}

func buildStartupInfoFromDaemon(info *model.DaemonInfo) uikit.StartupInfo {
	si := uikit.StartupInfo{}
	if info == nil {
		return si
	}
	si.Version = info.Version
	si.Fingerprint = info.Fingerprint
	si.Port = info.Port
	si.CloudEnabled = info.CloudEnabled
	si.ServiceManaged = info.ServiceManaged
	si.AuthDisabled = info.AuthDisabled
	si.ConfigStale = info.ConfigStale
	// The attach path used to drop these, so the header showed nothing until the
	// first /api/info poll landed — for findings that have no runs anywhere else in
	// the TUI, that meant a blank header on exactly the boot they mattered.
	si.ConfigWarnings = info.ConfigWarnings
	si.Timezone = info.ResolvedTimezone
	si.TimezoneSource = info.TimezoneSource

	for _, t := range info.Tasks {
		si.Tasks = append(si.Tasks, t)
	}
	si.Capabilities = info.Capabilities

	return si
}
