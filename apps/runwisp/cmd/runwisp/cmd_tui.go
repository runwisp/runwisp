// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/spf13/cobra"
)

var tuiPasswordStdin bool

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Connect a TUI to a running daemon",
	Long: `Launches the interactive terminal UI and connects it to an already-running
RunWisp daemon via its HTTP API.

Two authentication paths:

  Local (default).  When no password is supplied the TUI mints a short-lived
  JWT directly from the daemon's signing secret on disk. Anyone who can read
  the data dir can do this — filesystem perms are the trust boundary.

  Remote.  Pass --password-stdin or set RUNWISP_PASSWORD to authenticate via
  SRP. Use this when the daemon's data dir is not reachable (different user,
  different machine, container boundary).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUIClient()
	},
}

func init() {
	tuiCmd.Flags().BoolVar(&tuiPasswordStdin, "password-stdin", false, "read the operator password as a single line from stdin and authenticate via SRP")
}

// tuiAuthSource indicates which authentication path the TUI should take
// based on the operator's environment and flags. Split out for testability
// so the branch selection can be verified without a real daemon or stdin.
type tuiAuthSource struct {
	useStdin bool
	envSet   bool
}

func (s tuiAuthSource) remote() bool { return s.useStdin || s.envSet }

func resolveTUIAuthSource() tuiAuthSource {
	return tuiAuthSource{
		useStdin: tuiPasswordStdin,
		envSet:   os.Getenv("RUNWISP_PASSWORD") != "",
	}
}

func runTUIClient() error {
	src := resolveTUIAuthSource()

	var client *apiclient.Client
	var explicit bool

	if src.remote() {
		source := "env"
		if src.useStdin {
			source = "-"
		}
		password, err := readRemotePasswordFrom("RUNWISP_PASSWORD", source)
		if err != nil {
			return err
		}
		client = apiclient.New(localAPIBaseURL(), password)
		if err := client.AuthenticateSRP(); err != nil {
			if errors.Is(err, apiclient.ErrUnauthorized) {
				return tuiPasswordMismatchError(flags.Port, true)
			}
			if errors.Is(err, apiclient.ErrRateLimited) {
				return authRateLimitedError(flags.Port)
			}
			return fmt.Errorf("authenticate: %w", err)
		}
		explicit = true
	} else {
		c, err := newLocalAuthedClient()
		if err != nil {
			return fmt.Errorf("authentication setup failed: %w", err)
		}
		client = c
	}

	if err := pollHealth(client, 5*time.Second); err != nil {
		return fmt.Errorf("cannot reach daemon at %s — is it running? (%w)", localAPIBaseURL(), err)
	}

	if err := runTUIConnect(client); err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return tuiPasswordMismatchError(flags.Port, explicit)
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
		return err
	}
	return nil
}

// buildStartupInfoFromDaemon converts DaemonInfo into the TUI's StartupInfo.
// With either auth path the TUI never holds a long-term password, so the
// PasswordGenerated/Password fields stay zero.
func buildStartupInfoFromDaemon(info *model.DaemonInfo) tui.StartupInfo {
	si := tui.StartupInfo{}
	if info == nil {
		return si
	}
	si.Version = info.Version
	si.Fingerprint = info.Fingerprint
	si.Port = info.Port
	si.CloudEnabled = info.CloudEnabled
	si.Timezone = info.ResolvedTimezone
	si.TimezoneSource = info.TimezoneSource

	for _, t := range info.Tasks {
		si.Tasks = append(si.Tasks, t)
	}
	si.Capabilities = info.Capabilities

	return si
}
