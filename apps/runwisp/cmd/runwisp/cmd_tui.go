// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/spf13/cobra"
)

var tuiPassword string

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Connect a TUI to a running daemon",
	Long:  `Launches the interactive terminal UI and connects it to an already-running RunWisp daemon via its HTTP API.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUIClient()
	},
}

func init() {
	tuiCmd.Flags().StringVar(&tuiPassword, "password", "", "authentication password (or set RUNWISP_PASSWORD)")
}

func runTUIClient() error {
	passwordExplicit := tuiPassword != "" || os.Getenv("RUNWISP_PASSWORD") != ""
	password := tuiPassword
	if password == "" {
		resolved, _, err := datadir.ResolvePassword(flags.DataDir)
		if err != nil || resolved == "" {
			return fmt.Errorf("password required: use --password or RUNWISP_PASSWORD env var")
		}
		password = resolved
	}

	client := apiclient.New(localAPIBaseURL(), password)

	if err := pollHealth(client, 5*time.Second); err != nil {
		return fmt.Errorf("cannot reach daemon at %s — is it running? (%w)", localAPIBaseURL(), err)
	}

	err := runTUIConnect(client, password, passwordExplicit)
	if err != nil {
		if errors.Is(err, apiclient.ErrUnauthorized) {
			return tuiPasswordMismatchError(flags.Port, passwordExplicit)
		}
		if errors.Is(err, apiclient.ErrRateLimited) {
			return authRateLimitedError(flags.Port)
		}
	}
	return err
}

// buildStartupInfoFromDaemon converts DaemonInfo into the TUI's StartupInfo.
// passwordExplicit means the user supplied the password themselves; when false
// the password was auto-generated and should be disclosed in the TUI.
func buildStartupInfoFromDaemon(info *model.DaemonInfo, password string, passwordExplicit bool) tui.StartupInfo {
	si := tui.StartupInfo{
		Password:          password,
		PasswordGenerated: !passwordExplicit,
	}
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
