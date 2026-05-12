// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Connect a TUI to a running daemon",
	Long: `Launches the interactive terminal UI and connects it to an already-running
RunWisp daemon via its HTTP API.

Authentication uses the local-JWT shortcut: anyone who can read the data dir
can mint a short-lived token directly from the daemon's signing secret. No
password prompt — your filesystem perms are the trust boundary.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUIClient()
	},
}

func runTUIClient() error {
	client, err := newLocalAuthedClient()
	if err != nil {
		return fmt.Errorf("authentication setup failed: %w", err)
	}

	if err := pollHealth(client, 5*time.Second); err != nil {
		return fmt.Errorf("cannot reach daemon at %s — is it running? (%w)", localAPIBaseURL(), err)
	}

	if err := runTUIConnect(client); err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return tuiPasswordMismatchError(flags.Port, false)
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
		return err
	}
	return nil
}

// buildStartupInfoFromDaemon converts DaemonInfo into the TUI's StartupInfo.
// With the local-JWT shortcut the TUI never holds a password, so the
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
