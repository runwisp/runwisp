// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is alive",
	Long: `Pings the daemon over its local Unix socket to verify it is running and
responsive, prints a short system summary, and warns when runwisp.toml has
changed on disk since the daemon started (config changes apply on restart).`,
	Example: `  runwisp status
  runwisp status --json   # daemon + per-task snapshot as JSON`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd.OutOrStdout(), flags, statusJSON)
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit a machine-readable JSON document to stdout instead of the human summary")
}

func runStatus(out io.Writer, f Flags, asJSON bool) error {
	// The Unix socket is the local-trusted transport (same as exec/tui):
	// no password needed, and unlike a TCP probe it cannot hit a different
	// process that happens to squat on the port.
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := client.HealthCheck(); err != nil {
		if asJSON {
			// Emit an unhealthy document so an agent learns of the failure from
			// the JSON, not from stderr text; the returned error still drives
			// exit 1.
			if werr := writeJSON(out, statusJSONDoc{
				SchemaVersion: jsonSchemaVersion,
				Healthy:       false,
				Error:         err.Error(),
				Tasks:         []statusTaskJSON{},
			}); werr != nil {
				return werr
			}
		}
		return fmt.Errorf("daemon is not reachable at %s (%w) — %s", localAPISocketPath(f), err, daemonNotRunningHint)
	}

	if asJSON {
		return writeJSON(out, buildStatusDoc(client))
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

// buildStatusDoc assembles the live snapshot for `status --json`. Health is
// already confirmed by the caller; the remaining probes are soft — a failed
// info/system/tasks fetch leaves its fields at their zero value rather than
// failing the whole document, mirroring the human path's graceful degradation.
func buildStatusDoc(client *apiclient.Client) statusJSONDoc {
	doc := statusJSONDoc{
		SchemaVersion: jsonSchemaVersion,
		Healthy:       true,
		Tasks:         []statusTaskJSON{},
	}

	if info, err := client.GetDaemonInfo(); err == nil {
		doc.Version = info.Version
		doc.Port = info.Port
		doc.ExternalURL = info.ExternalURL
		doc.SchedulingActive = info.SchedulingActive
		doc.ConfigStale = info.ConfigStale
		doc.ResolvedTimezone = info.ResolvedTimezone
		doc.TimezoneSource = info.TimezoneSource
	}

	if stats, err := client.GetSystemStats(); err == nil {
		doc.System = newStatusSystem(stats)
	}

	if tasks, err := client.ListTasks(); err == nil {
		for _, tr := range tasks {
			doc.Tasks = append(doc.Tasks, newStatusTaskJSON(tr, lastRunOf(client, tr.Name)))
		}
	}

	return doc
}

// lastRunOf fetches a task's most recent run (default sort is created_at desc),
// or nil when the task has never run or the fetch fails.
func lastRunOf(client *apiclient.Client, taskName string) *model.Run {
	runs, _, err := client.ListRunsByTask(taskName, apiclient.RunsParams{Limit: 1})
	if err != nil || len(runs) == 0 {
		return nil
	}
	return &runs[0]
}

func printSystemStats(out io.Writer, stats *model.SystemStats) {
	fmt.Fprintf(out, "  Version:  %s\n", stats.Version)
	fmt.Fprintf(out, "  Uptime:   %s\n", stats.Uptime)
	fmt.Fprintf(out, "  CPU:      %d cores\n", stats.CPUCores)
	fmt.Fprintf(out, "  Host:     %s\n", stats.Host)
}
