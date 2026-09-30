// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/spf13/cobra"
)

// pausableTargets is the pause glob filter: cron tasks with manual_trigger on
// that no system cron daemon holds.
var pausableTargets = targetFilter{
	eligible: func(t model.TaskResponse) bool { return t.Pausable() && !t.Held() },
	noun:     "pausable cron task",
}

// pausedTargets is the resume glob filter, so `resume '*'` only reports the
// tasks it actually resumed.
var pausedTargets = targetFilter{
	eligible: func(t model.TaskResponse) bool { return t.PausedAt != nil },
	noun:     "paused task",
}

var pauseCmd = &cobra.Command{
	Use:   "pause <task...>",
	Short: "Pause the cron schedule of one or more tasks",
	Long: `Pauses the cron schedule of one or more tasks on a running daemon. A paused
task skips its cron ticks until 'runwisp resume': skipped ticks record no
runs and are not caught up on resume. Manual runs ('runwisp run', 'runwisp
start', the Web UI) keep working, and an active run is left alone ('runwisp
stop' cancels it).

The pause survives 'runwisp reload' and daemon restarts. A reload that removes
the task's cron, turns it into a service, or sets manual_trigger = false
clears it.

A target is a task name or a quoted shell-style glob ('backup-*', or '*' for
every pausable task). A task locked with manual_trigger = false is rejected; a
glob skips services, tasks without cron, and locked or held tasks instead of
failing.

With --url (or RUNWISP_URL), tasks are paused on a remote daemon instead, with
the same CHAP login and session caching as 'runwisp run --url'.`,
	Example: `  runwisp pause nightly-backup
  runwisp pause 'report-*'
  runwisp pause '*' --url https://ci.example.com --password "$RUNWISP_PASSWORD"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return controlTargets(cmd, flags, controlRemote, args, "pause", "paused", (*apiclient.Client).PauseTask, nil, pausableTargets)
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume <task...>",
	Short: "Resume the cron schedule of one or more paused tasks",
	Long: `Resumes the cron schedule of tasks paused with 'runwisp pause'. Each task
fires again from its next tick; ticks skipped while paused are not caught up.
Resuming a task that isn't paused is a no-op; a glob matches only paused
tasks.

Targets and --url otherwise work as for 'runwisp pause'.`,
	Example: `  runwisp resume nightly-backup
  runwisp resume '*'`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return controlTargets(cmd, flags, controlRemote, args, "resume", "resumed", (*apiclient.Client).ResumeTask, nil, pausedTargets)
	},
}

func init() {
	addRemoteFlags(pauseCmd)
	addRemoteFlags(resumeCmd)
}
