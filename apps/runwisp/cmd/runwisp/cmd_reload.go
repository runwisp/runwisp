// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload runwisp.toml into the running daemon",
	Long: `Re-read runwisp.toml and reconcile the running daemon's task set —
adding, changing, and removing tasks without a full restart.

Reload is validate-first: the whole config is loaded and validated before
anything live is touched. If it fails to parse/validate, or changes a
restart-only setting ([daemon], [scheduler] timezone, [storage], [notify]),
the reload is rejected and the running task set is left exactly as it was.

Added tasks do not fire run_on_start and are not caught up for ticks they
"missed" before existing — reload is not a restart. In-flight cron runs finish
under the definition they started with.

This is equivalent to sending the daemon a SIGHUP.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runReload(cmd, flags)
	},
}

func runReload(cmd *cobra.Command, f Flags) error {
	return reloadRunningDaemon(f, cmd.OutOrStdout())
}

// reloadRunningDaemon is the cobra-free half, so callers without a command can
// reload too — internal/cutover hands the held cron jobs to a daemon that was
// already running, and the first-run flow has no *cobra.Command to hand it.
func reloadRunningDaemon(f Flags, out io.Writer) error {
	if !isDaemonRunning(f) {
		fmt.Fprintf(out, "No daemon is running on data dir %s — nothing to reload.\n", absPathOrFallback(f.DataDir))
		return nil
	}

	client := apiclient.NewUnix(localAPISocketPath(f))
	result, err := client.Reload()
	if err != nil {
		return err
	}
	printReloadResult(out, result)
	return nil
}

// printReloadResult renders the diff the daemon applied. An empty diff is the
// common "edits already match" case and says so rather than printing nothing.
//
// Warnings print either way, and after the diff: a reload that changed no tasks
// because a crontab job stopped being schedulable is exactly the case where the
// operator needs to be told something, and IsEmpty deliberately doesn't count
// them so the "no task changes" line stays honest about the task set.
func printReloadResult(out io.Writer, result *model.ReloadResult) {
	if result == nil {
		fmt.Fprintln(out, "Configuration reloaded — no task changes.")
		return
	}
	if result.IsEmpty() {
		fmt.Fprintln(out, "Configuration reloaded — no task changes.")
	} else {
		fmt.Fprintln(out, "Configuration reloaded.")
		for _, name := range result.Added {
			fmt.Fprintf(out, "  + added   %s\n", name)
		}
		for _, c := range result.Changed {
			fmt.Fprintf(out, "  ~ changed %s (%s)\n", c.Name, strings.Join(c.Reasons, ", "))
		}
		for _, name := range result.Removed {
			fmt.Fprintf(out, "  - removed %s\n", name)
		}
	}
	for _, w := range result.Warnings {
		fmt.Fprintf(out, "  ! %s\n", w)
	}
}
