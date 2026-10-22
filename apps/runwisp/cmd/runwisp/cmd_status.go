// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
	"github.com/spf13/cobra"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check if the daemon is alive",
	Long: `Pings the daemon over its local Unix socket to verify it is running and
responsive, prints a short system summary, lists services that gave up
restarting, and warns when runwisp.toml has changed on disk since the daemon
started (apply them with 'runwisp reload').

With --url (or RUNWISP_URL), it asks a remote daemon instead, with the same
CHAP login and session caching as 'runwisp run --url'.`,
	Example: `  runwisp status
  runwisp status --json   # daemon + per-task snapshot as JSON
  runwisp status --url https://rw.example.com`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd.Context(), cmd.OutOrStdout(), flags, controlRemote, statusJSON)
	},
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "emit a machine-readable JSON document to stdout instead of the human summary")
	addRemoteFlags(statusCmd)
}

func runStatus(ctx context.Context, out io.Writer, f Flags, rf remoteFlags, asJSON bool) error {
	// Locally, the Unix socket is the trusted transport (same as exec/tui):
	// no password needed, and unlike a TCP probe it cannot hit a different
	// process that happens to squat on the port.
	baseURL, password := rf.resolve()
	client, err := connectDaemon(ctx, f, baseURL, password)
	if err == nil && baseURL != "" {
		// A cached session may have expired. Log in again now, or every probe
		// below would fail quietly and status would show a bare "healthy".
		err = withSessionRetry(ctx, client, baseURL, password, func() error {
			_, err := client.GetDaemonInfo(ctx)
			return err
		})
	}
	if err != nil {
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
		return err
	}

	if asJSON {
		return writeJSON(out, buildStatusDoc(ctx, client))
	}

	info, infoErr := client.GetDaemonInfo(ctx)
	if infoErr == nil {
		fmt.Fprintf(out, "RunWisp is healthy at :%d\n", info.Port)
		printWebUIAddress(out, info)
	} else {
		fmt.Fprintln(out, "RunWisp is healthy")
	}

	if stats, err := client.GetSystemStats(ctx); err == nil {
		printSystemStats(out, stats)
	}

	if infoErr == nil && info.ConfigStale {
		fmt.Fprintln(out, "\n⚠ runwisp.toml has changed since it was loaded, run 'runwisp reload' to apply")
	}
	// Findings the daemon printed at boot, re-derived from the live config so a
	// reload's are shown instead. Chiefly crontab jobs include_cron couldn't
	// schedule: they have no runs, so `status` is one of the few places they exist.
	if infoErr == nil {
		for _, w := range info.ConfigWarnings {
			fmt.Fprintf(out, "! %s\n", w)
		}
		// Held tasks come from the daemon's own task list, so this reports what the
		// live scheduler is actually doing rather than re-probing cron from here.
		// They have no runs — `status` is one of the few places they exist at all.
		printHeldBlock(out, heldTaskNames(info.Tasks), "")
	}
	if tasks, err := client.ListTasks(ctx); err == nil {
		printPausedBlock(out, pausedTaskNames(tasks))
		printDownServices(out, tasks)
	}
	return nil
}

// printDownServices lists the services with an instance that gave up
// restarting (FATAL). Nothing respawns those until an operator starts them,
// so status names them and the command that does.
func printDownServices(out io.Writer, tasks []model.TaskResponse) {
	var down, names []string
	for _, t := range tasks {
		if t.Service == nil || !hasFatalInstance(t.Service) {
			continue
		}
		down = append(down, fmt.Sprintf("%s (%s, %d/%d running)", t.Name,
			strings.ToUpper(t.Service.State), t.Service.RunningInstances, t.Service.DesiredInstances))
		names = append(names, t.Name)
	}
	if len(down) == 0 {
		return
	}
	n := len(down)
	fmt.Fprintf(out, "\n✗ %s %s down: %s\n", textutil.Count(n, "service", "services"), textutil.Pluralize(n, "is", "are"), strings.Join(down, ", "))
	fmt.Fprintf(out, "  Run 'runwisp start %s' once the cause is fixed.\n", strings.Join(names, " "))
}

func hasFatalInstance(s *model.ServiceStatus) bool {
	if s.State == "fatal" {
		return true
	}
	for _, inst := range s.Instances {
		if inst.State == "fatal" {
			return true
		}
	}
	return false
}

// buildStatusDoc assembles the live snapshot for `status --json`. Health is
// already confirmed by the caller; the remaining probes are soft — a failed
// info/system/tasks fetch leaves its fields at their zero value rather than
// failing the whole document, mirroring the human path's graceful degradation.
func buildStatusDoc(ctx context.Context, client *apiclient.Client) statusJSONDoc {
	doc := statusJSONDoc{
		SchemaVersion: jsonSchemaVersion,
		Healthy:       true,
		Tasks:         []statusTaskJSON{},
	}

	if info, err := client.GetDaemonInfo(ctx); err == nil {
		doc.Version = info.Version
		doc.Port = info.Port
		doc.ExternalURL = info.ExternalURL
		doc.ExternalURLSource = info.ExternalURLSource
		doc.SchedulingActive = info.SchedulingActive
		doc.ConfigStale = info.ConfigStale
		doc.ConfigWarnings = info.ConfigWarnings
		doc.ResolvedTimezone = info.ResolvedTimezone
		doc.TimezoneSource = info.TimezoneSource
	}

	if stats, err := client.GetSystemStats(ctx); err == nil {
		doc.System = newStatusSystem(stats)
	}

	if tasks, err := client.ListTasks(ctx); err == nil {
		for _, tr := range tasks {
			doc.Tasks = append(doc.Tasks, newStatusTaskJSON(tr, lastRunOf(ctx, client, tr.Name)))
		}
	}

	return doc
}

// lastRunOf fetches a task's most recent run (default sort is created_at desc),
// or nil when the task has never run or the fetch fails.
func lastRunOf(ctx context.Context, client *apiclient.Client, taskName string) *model.Run {
	runs, _, err := client.ListRuns(ctx, apiclient.RunsParams{TaskName: taskName, Limit: 1})
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

// printWebUIAddress names the address notification links point at, and where
// it came from, so an operator can tell a pinned external_url from one the
// daemon picked up from a sign-in.
func printWebUIAddress(out io.Writer, info *model.DaemonInfo) {
	switch info.ExternalURLSource {
	case "config":
		fmt.Fprintf(out, "Web UI: %s (external_url)\n", info.ExternalURL)
	case "detected":
		fmt.Fprintf(out, "Web UI: %s (from the last sign-in; set external_url to pin it)\n", info.ExternalURL)
	}
}
