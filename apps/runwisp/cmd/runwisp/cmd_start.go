// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start <target...>",
	Short: "Start one or more tasks/services",
	Long: `Starts one or more tasks or services against a running daemon.

A target is a task name, a service name, or a quoted shell-style glob matched
against task and service names ('web*', or '*' for everything). Several
targets can be given at once. For a service, this un-parks it (clearing an
operator stop or a give-up) and fills empty instance slots — already-running
instances are left alone. For a task, this triggers a run, unless one is
already active or queued, in which case it's a no-op. A target locked with
manual_trigger = false is rejected (403); a glob silently skips locked
entries instead of failing.

With --url (or RUNWISP_URL), targets are started on a remote daemon instead —
the same CHAP login and session caching as 'runwisp run --url'.

'runwisp start' never returns a run ID; use 'runwisp run --detach' when you
need one.`,
	Example: `  runwisp start web
  runwisp start web worker 'batch-*'
  runwisp start '*' --url https://ci.example.com --password "$RUNWISP_PASSWORD"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		start := func(c *apiclient.Client, ctx context.Context, name string) error {
			return c.StartTask(ctx, name, "cli")
		}
		return controlTargets(cmd, flags, controlRemote, args, "start", "started", start, nil)
	},
}

func init() {
	addRemoteFlags(startCmd)
}
