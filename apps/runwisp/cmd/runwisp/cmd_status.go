// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is alive",
	Long: `Pings the daemon over its local Unix socket to verify it is running and
responsive, prints a short system summary, and warns when runwisp.toml has
changed on disk since the daemon started (config changes apply on restart).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd.OutOrStdout())
	},
}

func runStatus(out io.Writer) error {
	// The Unix socket is the local-trusted transport (same as exec/tui):
	// no password needed, and unlike a TCP probe it cannot hit a different
	// process that happens to squat on the port.
	client := apiclient.NewUnix(localAPISocketPath())
	if err := client.HealthCheck(); err != nil {
		return fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(), err, daemonNotRunningHint)
	}

	info, infoErr := client.GetDaemonInfo()
	if infoErr == nil {
		fmt.Fprintf(out, "RunWisp is healthy at :%d\n", info.Port)
	} else {
		fmt.Fprintln(out, "RunWisp is healthy")
	}

	if stats, err := client.GetSystemStats(); err == nil {
		printSystemStats(out, stats)
	}

	if infoErr == nil && info.ConfigStale {
		fmt.Fprintln(out, "\n⚠ runwisp.toml has changed since the daemon started — run 'runwisp restart' to apply")
	}
	return nil
}

func printSystemStats(out io.Writer, stats *model.SystemStats) {
	fmt.Fprintf(out, "  Version:  %s\n", stats.Version)
	fmt.Fprintf(out, "  Uptime:   %s\n", stats.Uptime)
	fmt.Fprintf(out, "  CPU:      %d cores\n", stats.CPUCores)
	fmt.Fprintf(out, "  Host:     %s\n", stats.Host)
}
