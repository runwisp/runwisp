// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Connect a TUI to a running daemon",
	Long:  `Launches the interactive terminal UI and connects it to an already-running RunWisp daemon via its Unix socket (no password required — access is gated by data-dir filesystem permissions).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUIClient(flags)
	},
}

func runTUIClient(f Flags) error {
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

// buildStartupInfoFromDaemon converts DaemonInfo into the TUI's StartupInfo.
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
	si.Timezone = info.ResolvedTimezone
	si.TimezoneSource = info.TimezoneSource

	for _, t := range info.Tasks {
		si.Tasks = append(si.Tasks, t)
	}
	si.Capabilities = info.Capabilities

	return si
}
